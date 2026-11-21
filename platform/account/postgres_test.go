package account

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wricardo/drbrain/platform/pgdb/pgtest"
)

func newPG(t *testing.T) *PostgresStore {
	t.Helper()
	return NewPostgresStore(pgtest.Pool(t))
}

// accountID builds a valid 24-hex id from n.
func accountID(n int) string { return fmt.Sprintf("%024x", n) }

func TestPostgresCreateAndLookups(t *testing.T) {
	s := newPG(t)
	a := acct(id1, "otter", "google", "u1")
	a.DisplayName = "Otter Fan"
	a.PasswordHash = "$argon2id$x"
	a.HandleChangedAt = time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	if err := s.Create(a); err != nil {
		t.Fatal(err)
	}
	for name, get := range map[string]func() (Account, bool){
		"Get":         func() (Account, bool) { return s.Get(id1) },
		"ByIdentity":  func() (Account, bool) { return s.ByIdentity("google", "u1") },
		"ByHandle":    func() (Account, bool) { return s.ByHandle("otter") },
		"ByHandle ci": func() (Account, bool) { return s.ByHandle("OTTER") },
	} {
		got, ok := get()
		if !ok || got != a {
			t.Errorf("%s = %+v, %v; want %+v", name, got, ok, a)
		}
	}
	if _, ok := s.Get(id2); ok {
		t.Error("Get found an account that does not exist")
	}
	if _, ok := s.Get("not-an-id"); ok {
		t.Error("Get accepted a malformed id")
	}
	if s.Count() != 1 {
		t.Errorf("Count = %d", s.Count())
	}
	// A never-changed handle round-trips as the zero time.
	if err := s.Create(acct(id2, "fox", "github", "u2")); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(id2); !got.HandleChangedAt.IsZero() {
		t.Errorf("HandleChangedAt = %v, want zero", got.HandleChangedAt)
	}
}

func TestPostgresCreateEnforcesUniqueHandleAndIdentity(t *testing.T) {
	s := newPG(t)
	if err := s.Create(acct(id1, "otter", "google", "u1")); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		a    Account
		want error
	}{
		{"same handle", acct(id2, "otter", "github", "u2"), ErrHandleTaken},
		{"same handle other case", acct(id2, "OTTER", "github", "u2"), ErrHandleTaken},
		{"same identity", acct(id2, "fox", "google", "u1"), ErrIdentityExists},
		{"same id", acct(id1, "fox", "github", "u2"), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := s.Create(tt.a)
			if err == nil {
				t.Fatal("duplicate accepted")
			}
			if tt.want != nil && !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
	if s.Count() != 1 {
		t.Fatalf("Count = %d after rejected creates", s.Count())
	}
}

// The database, not a Go mutex, decides who gets a handle: of many concurrent
// sign-ups for one handle exactly one wins, and the rest see ErrHandleTaken.
func TestPostgresConcurrentCreateOneHandleOneWinner(t *testing.T) {
	s := newPG(t)
	const n = 12
	var wins, taken atomic.Int32
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			handle := "racer"
			if i%2 == 1 {
				handle = "RACER"
			}
			switch err := s.Create(acct(accountID(i+1), handle, "password", fmt.Sprint("user", i))); {
			case err == nil:
				wins.Add(1)
			case errors.Is(err, ErrHandleTaken):
				taken.Add(1)
			default:
				t.Errorf("Create: %v", err)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 || taken.Load() != n-1 {
		t.Fatalf("wins=%d taken=%d, want 1 and %d", wins.Load(), taken.Load(), n-1)
	}
}

func TestPostgresConcurrentCreateOneIdentityOneWinner(t *testing.T) {
	s := newPG(t)
	const n = 10
	var wins, exists atomic.Int32
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			switch err := s.Create(acct(accountID(i+1), fmt.Sprint("name", i), "google", "same-user")); {
			case err == nil:
				wins.Add(1)
			case errors.Is(err, ErrIdentityExists):
				exists.Add(1)
			default:
				t.Errorf("Create: %v", err)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 || exists.Load() != n-1 {
		t.Fatalf("wins=%d exists=%d", wins.Load(), exists.Load())
	}
}

func TestPostgresUpdate(t *testing.T) {
	s := newPG(t)
	if err := s.Create(acct(id1, "otter", "google", "u1")); err != nil {
		t.Fatal(err)
	}
	if err := s.Create(acct(id2, "fox", "github", "u2")); err != nil {
		t.Fatal(err)
	}

	changed := time.Date(2026, 5, 6, 7, 8, 9, 0, time.UTC)
	got, err := s.Update(id1, func(a *Account) error {
		a.Handle = "Seal"
		a.DisplayName = "Seal!"
		a.ProfilePublic = false
		a.HandleChangedAt = changed
		a.Provider, a.ProviderUserID = "evil", "other" // identity must not change
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Handle != "seal" || got.DisplayName != "Seal!" || got.ProfilePublic || got.Provider != "google" || got.ProviderUserID != "u1" {
		t.Fatalf("Update returned %+v", got)
	}
	if again, _ := s.ByHandle("seal"); again != got || !again.HandleChangedAt.Equal(changed) {
		t.Fatalf("stored %+v differs from returned %+v", again, got)
	}
	if _, ok := s.ByHandle("otter"); ok {
		t.Error("old handle still resolves")
	}

	if _, err := s.Update(id2, func(a *Account) error { a.Handle = "SEAL"; return nil }); !errors.Is(err, ErrHandleTaken) {
		t.Fatalf("taking a used handle: %v", err)
	}
	boom := errors.New("boom")
	if _, err := s.Update(id2, func(*Account) error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("fn error not returned: %v", err)
	}
	if _, err := s.Update(accountID(99), func(*Account) error { return nil }); err == nil {
		t.Fatal("updating a missing account succeeded")
	}
	if fox, _ := s.Get(id2); fox.Handle != "fox" {
		t.Fatalf("failed update changed the account: %+v", fox)
	}
}

// Concurrent updates of one account serialise on the row lock: every increment
// of the counter-like field survives.
func TestPostgresConcurrentUpdatesDoNotLoseWrites(t *testing.T) {
	s := newPG(t)
	if err := s.Create(acct(id1, "otter", "google", "u1")); err != nil {
		t.Fatal(err)
	}
	const n = 10
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Update(id1, func(a *Account) error {
				a.DisplayName += "x"
				return nil
			}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if got, _ := s.Get(id1); got.DisplayName != "otterxxxxxxxxxx" {
		t.Fatalf("DisplayName = %q, want ten appended x", got.DisplayName)
	}
}

func TestPostgresDeleteKeepsSessionsAsGuests(t *testing.T) {
	pool := pgtest.Pool(t)
	s := NewPostgresStore(pool)
	if err := s.Create(acct(id1, "otter", "google", "u1")); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `
INSERT INTO sessions (id, map_id, map, program, vm, speed_ms, seq, created_at, last_action_at, attempts, best_steps, account_id)
VALUES ('00112233445566aa', 'm', '{}', '{}', '{}', 500, 1, now(), now(), 0, 0, $1)`, id1); err != nil {
		t.Fatal(err)
	}

	if err := s.Delete(id1); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get(id1); ok {
		t.Error("account still found")
	}
	if _, ok := s.ByHandle("otter"); ok {
		t.Error("handle still resolves")
	}
	var link *string
	if err := pool.QueryRow(context.Background(), `SELECT account_id FROM sessions WHERE id = '00112233445566aa'`).Scan(&link); err != nil {
		t.Fatal(err)
	}
	if link != nil {
		t.Errorf("session still linked to %q", *link)
	}
	if err := s.Delete(id1); err == nil {
		t.Error("deleting twice succeeded")
	}
	// The freed handle and identity can be used again.
	if err := s.Create(acct(id2, "otter", "google", "u1")); err != nil {
		t.Errorf("re-create after delete: %v", err)
	}
}

func TestPostgresExportListings(t *testing.T) {
	s := newPG(t)
	a, b := acct(id1, "otter", "google", "u1"), acct(id2, "fox", "github", "u2")
	b.CreatedAt = a.CreatedAt.Add(time.Hour)
	for _, x := range []Account{b, a} {
		if err := s.Create(x); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Accounts()
	if err != nil || len(got) != 2 || got[0].ID != id1 || got[1].ID != id2 {
		t.Fatalf("Accounts = %+v, %v (want oldest first)", got, err)
	}
}
