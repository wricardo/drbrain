package account

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wricardo/drbrain/games/motor/game/service"
)

const (
	id1 = "0123456789abcdef01234567"
	id2 = "fedcba9876543210fedcba98"
)

func hash(c byte) string { return strings.Repeat(string(c), 64) }

func acct(id, handle, provider, user string) service.Account {
	return service.Account{
		ID: id, Provider: provider, ProviderUserID: user, Handle: handle, DisplayName: handle,
		ProfilePublic: true, CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}
}

func open(t *testing.T, dir string, now func() time.Time) *Store {
	t.Helper()
	s, err := Open(dir, now)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

func TestCreateEnforcesUniqueHandleAndIdentity(t *testing.T) {
	s := open(t, t.TempDir(), nil)
	if err := s.Create(acct(id1, "otter", "google", "u1")); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		a    service.Account
		want error
	}{
		{"same handle", acct(id2, "otter", "github", "u2"), service.ErrHandleTaken},
		{"same handle other case", acct(id2, "OTTER", "github", "u2"), service.ErrHandleTaken},
		{"same identity", acct(id2, "fox", "google", "u1"), service.ErrIdentityExists},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := s.Create(tc.a); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
	// Same user id under another provider is a different identity.
	if err := s.Create(acct(id2, "fox", "github", "u1")); err != nil {
		t.Fatalf("other provider, same user id: %v", err)
	}
	if s.Count() != 2 {
		t.Fatalf("Count = %d, want 2", s.Count())
	}
}

func TestCreateRejectsBadIDsBeforeTouchingDisk(t *testing.T) {
	dir := t.TempDir()
	s := open(t, dir, nil)
	for _, id := range []string{"", "../../etc/passwd", "ABCDEF0123456789ABCDEF01", "short", id1 + "0"} {
		if err := s.Create(acct(id, "otter", "google", "u1")); err == nil {
			t.Errorf("Create(id %q) succeeded", id)
		}
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "accounts"))
	if len(entries) != 0 {
		t.Fatalf("files written for invalid ids: %v", entries)
	}
	if err := s.Delete("../x"); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("Delete(../x) = %v", err)
	}
	if err := s.PutLogin(service.Login{TokenHash: "../x", AccountID: id1}); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("PutLogin bad hash = %v", err)
	}
	if err := s.DeleteLogin("../x"); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("DeleteLogin bad hash = %v", err)
	}
}

func TestPersistenceRoundTrip(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	s := open(t, dir, clock)
	a := acct(id1, "otter", "google", "sub-1")
	a.DisplayName, a.ProfilePublic = "Otter Fan", false
	a.HandleChangedAt = now.Add(-time.Hour)
	if err := s.Create(a); err != nil {
		t.Fatal(err)
	}
	if err := s.PutLogin(service.Login{TokenHash: hash('a'), AccountID: id1, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}

	s2 := open(t, dir, clock)
	got, ok := s2.ByHandle("OTTER")
	if !ok || got != a {
		t.Fatalf("ByHandle after reload = %+v, %v; want %+v", got, ok, a)
	}
	if byID, _ := s2.ByIdentity("google", "sub-1"); byID.ID != id1 {
		t.Fatalf("ByIdentity = %+v", byID)
	}
	if l, ok := s2.GetLogin(hash('a')); !ok || l.AccountID != id1 {
		t.Fatalf("login not reloaded: %+v %v", l, ok)
	}
}

// Nothing but the minimum is on disk: no email, no avatar, no raw token.
func TestFilesHoldOnlyMinimalData(t *testing.T) {
	dir := t.TempDir()
	s := open(t, dir, nil)
	if err := s.Create(acct(id1, "otter", "google", "sub-1")); err != nil {
		t.Fatal(err)
	}
	const token = "super-secret-cookie-value-0123456789"
	if err := s.PutLogin(service.Login{
		TokenHash: service.HashToken(token), AccountID: id1,
		CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	var all strings.Builder
	_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			b, _ := os.ReadFile(p)
			all.WriteString(p + "\n" + string(b) + "\n")
		}
		return nil
	})
	if strings.Contains(all.String(), token) {
		t.Fatal("the raw login token is on disk")
	}
	if _, err := os.Stat(filepath.Join(dir, "logins", service.HashToken(token)+".json")); err != nil {
		t.Fatalf("login file is not named by the token hash: %v", err)
	}
	for _, banned := range []string{"email", "avatar", "picture", "photo", "locale"} {
		if strings.Contains(strings.ToLower(all.String()), banned) {
			t.Errorf("stored data mentions %q", banned)
		}
	}
}

func TestUpdateHandleChangeAndConflict(t *testing.T) {
	s := open(t, t.TempDir(), nil)
	_ = s.Create(acct(id1, "otter", "google", "u1"))
	_ = s.Create(acct(id2, "fox", "google", "u2"))

	if _, err := s.Update(id1, func(a *service.Account) error { a.Handle = "fox"; return nil }); !errors.Is(err, service.ErrHandleTaken) {
		t.Fatalf("taking another handle: %v", err)
	}
	got, err := s.Update(id1, func(a *service.Account) error {
		a.Handle, a.ProfilePublic = "badger", false
		a.Provider = "evil" // identity is immutable
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Handle != "badger" || got.ProfilePublic || got.Provider != "google" {
		t.Fatalf("Update = %+v", got)
	}
	if _, ok := s.ByHandle("otter"); ok {
		t.Fatal("old handle still resolves")
	}
	// The old handle is free again.
	if _, err := s.Update(id2, func(a *service.Account) error { a.Handle = "otter"; return nil }); err != nil {
		t.Fatalf("reusing a released handle: %v", err)
	}
	// An error from fn changes nothing.
	boom := errors.New("boom")
	if _, err := s.Update(id2, func(a *service.Account) error { a.Handle = "zzz"; return boom }); !errors.Is(err, boom) {
		t.Fatal(err)
	}
	if a, _ := s.Get(id2); a.Handle != "otter" {
		t.Fatalf("failed update changed the account: %+v", a)
	}
}

func TestDeleteRemovesAccountAndLogins(t *testing.T) {
	dir := t.TempDir()
	s := open(t, dir, nil)
	_ = s.Create(acct(id1, "otter", "google", "u1"))
	future := time.Now().Add(time.Hour)
	_ = s.PutLogin(service.Login{TokenHash: hash('a'), AccountID: id1, CreatedAt: time.Now(), ExpiresAt: future})
	_ = s.PutLogin(service.Login{TokenHash: hash('b'), AccountID: id1, CreatedAt: time.Now(), ExpiresAt: future})

	if err := s.Delete(id1); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get(id1); ok {
		t.Fatal("account survived")
	}
	if _, ok := s.ByHandle("otter"); ok {
		t.Fatal("handle still indexed")
	}
	if _, ok := s.ByIdentity("google", "u1"); ok {
		t.Fatal("identity still indexed")
	}
	if _, ok := s.GetLogin(hash('a')); ok {
		t.Fatal("login survived")
	}
	for _, sub := range []string{"accounts", "logins"} {
		if entries, _ := os.ReadDir(filepath.Join(dir, sub)); len(entries) != 0 {
			t.Fatalf("%s not empty: %v", sub, entries)
		}
	}
	if err := s.Delete(id1); err == nil {
		t.Fatal("deleting twice should report not found")
	}
	// The handle and identity can be used again.
	if err := s.Create(acct(id2, "otter", "google", "u1")); err != nil {
		t.Fatalf("re-create after delete: %v", err)
	}
}

func TestLoginsExpireOnLoadAndAreCapped(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	s := open(t, dir, clock)
	_ = s.Create(acct(id1, "otter", "google", "u1"))
	_ = s.PutLogin(service.Login{TokenHash: hash('a'), AccountID: id1, CreatedAt: now, ExpiresAt: now.Add(time.Hour)})

	later := func() time.Time { return now.Add(2 * time.Hour) }
	s2 := open(t, dir, later)
	if _, ok := s2.GetLogin(hash('a')); ok {
		t.Fatal("expired login was loaded")
	}
	if _, err := os.Stat(filepath.Join(dir, "logins", hash('a')+".json")); !os.IsNotExist(err) {
		t.Fatal("expired login file was not cleaned up")
	}

	// Cap: the oldest logins are dropped beyond MaxLoginsPerAccount.
	for i := 0; i < service.MaxLoginsPerAccount+3; i++ {
		h := fmt.Sprintf("%064x", i+1)
		if err := s2.PutLogin(service.Login{TokenHash: h, AccountID: id1, CreatedAt: now.Add(time.Duration(i) * time.Second), ExpiresAt: now.Add(10 * time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := s2.GetLogin(fmt.Sprintf("%064x", 1)); ok {
		t.Error("oldest login was not evicted")
	}
	newest := fmt.Sprintf("%064x", service.MaxLoginsPerAccount+3)
	if _, ok := s2.GetLogin(newest); !ok {
		t.Error("newest login was evicted")
	}
	files, _ := os.ReadDir(filepath.Join(dir, "logins"))
	if len(files) != service.MaxLoginsPerAccount {
		t.Fatalf("%d login files, want %d", len(files), service.MaxLoginsPerAccount)
	}
}

func TestLoadSkipsCorruptAndDuplicateFiles(t *testing.T) {
	dir := t.TempDir()
	s := open(t, dir, nil)
	_ = s.Create(acct(id1, "otter", "google", "u1"))
	// Corrupt file, misnamed file, and a stray temp file.
	_ = os.WriteFile(filepath.Join(dir, "accounts", id2+".json"), []byte("{not json"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "accounts", ".tmp-x"), []byte("x"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "accounts", "notanid.json"), []byte("{}"), 0o600)
	// A second file with the same handle as the first must not shadow it.
	dup, _ := os.ReadFile(filepath.Join(dir, "accounts", id1+".json"))
	dupText := strings.NewReplacer(id1, "aaaaaaaaaaaaaaaaaaaaaaaa", `"u1"`, `"u9"`).Replace(string(dup))
	_ = os.WriteFile(filepath.Join(dir, "accounts", "aaaaaaaaaaaaaaaaaaaaaaaa.json"), []byte(dupText), 0o600)

	s2 := open(t, dir, nil)
	if s2.Count() != 1 {
		t.Fatalf("Count = %d, want 1", s2.Count())
	}
	if a, _ := s2.ByHandle("otter"); a.ID != id1 {
		t.Fatalf("handle owner = %s, want %s", a.ID, id1)
	}
}
