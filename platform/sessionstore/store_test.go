package sessionstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// payload is a stand-in for a game's encoded session.
type payload struct {
	ID      string    `json:"id"`
	Created time.Time `json:"created_at"`
	Action  time.Time `json:"last_action_at"`
	Value   int       `json:"value"`
}

func encode(p payload) []byte {
	raw, _ := json.Marshal(p)
	return raw
}

func summarize(raw []byte) (Meta, error) {
	var p payload
	if err := json.Unmarshal(raw, &p); err != nil {
		return Meta{}, err
	}
	return Meta{
		ID: p.ID, CreatedAt: p.Created, LastActionAt: p.Action, Seq: uint64(p.Value),
	}, nil
}

func metaFor(p payload) Meta {
	return Meta{ID: p.ID, CreatedAt: p.Created, LastActionAt: p.Action, Seq: uint64(p.Value)}
}

func TestFilePersistReload(t *testing.T) {
	dir := t.TempDir()
	p := payload{ID: "abc", Created: time.Unix(100, 0), Action: time.Unix(200, 0), Value: 7}

	s, err := Open(Options{Dir: dir, Summary: summarize})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Persist(metaFor(p), encode(p)); err != nil {
		t.Fatal(err)
	}
	if s.Count() != 1 {
		t.Errorf("Count = %d, want 1", s.Count())
	}

	// A new process: nothing loaded, the file is discovered by List, and Load
	// returns the identical payload.
	s2, err := Open(Options{Dir: dir, Summary: summarize})
	if err != nil {
		t.Fatal(err)
	}
	list, err := s2.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != "abc" || list[0].Loaded {
		t.Fatalf("List = %+v, want abc loaded=false", list)
	}
	got, raw, err := s2.Load("abc")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "abc" || string(raw) != string(encode(p)) {
		t.Errorf("Load = %+v %s", got, raw)
	}
	if s2.Count() != 1 {
		t.Errorf("Count after Load = %d", s2.Count())
	}
}

// TestLegacyRawFile proves an old payload-only file (no platform envelope) is
// listed and loaded through the game's Summary.
func TestLegacyRawFile(t *testing.T) {
	dir := t.TempDir()
	old := payload{ID: "legacy01", Created: time.Unix(10, 0), Action: time.Unix(20, 0), Value: 3}
	if err := os.WriteFile(filepath.Join(dir, "legacy01.json"), encode(old), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Open(Options{Dir: dir, Summary: summarize})
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != "legacy01" || list[0].Loaded {
		t.Fatalf("List = %+v", list)
	}
	_, raw, err := s.Load("legacy01")
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != string(encode(old)) {
		t.Errorf("legacy payload changed: %s", raw)
	}
}

func TestLimitRejectsAndEvicts(t *testing.T) {
	now := time.Unix(1000, 0)
	newPayload := func(id string, at time.Time) (Meta, []byte) {
		p := payload{ID: id, Created: at, Action: at}
		return metaFor(p), encode(p)
	}

	t.Run("reject", func(t *testing.T) {
		s, err := Open(Options{Dir: t.TempDir(), MaxSessions: 2, Summary: summarize})
		if err != nil {
			t.Fatal(err)
		}
		m1, b1 := newPayload("one", now)
		m2, b2 := newPayload("two", now.Add(time.Second))
		m3, b3 := newPayload("three", now.Add(2*time.Second))
		for _, pair := range []struct {
			m Meta
			b []byte
		}{{m1, b1}, {m2, b2}} {
			if err := s.Persist(pair.m, pair.b); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.Persist(m3, b3); !errors.Is(err, ErrLimit) {
			t.Fatalf("third Persist err = %v, want ErrLimit", err)
		}
	})

	t.Run("evict oldest", func(t *testing.T) {
		s, err := Open(Options{Dir: t.TempDir(), MaxSessions: 2, Evict: true, Summary: summarize})
		if err != nil {
			t.Fatal(err)
		}
		m1, b1 := newPayload("one", now)
		m2, b2 := newPayload("two", now.Add(time.Second))
		m3, b3 := newPayload("three", now.Add(2*time.Second))
		_ = s.Persist(m1, b1)
		_ = s.Persist(m2, b2)
		if err := s.Persist(m3, b3); err != nil {
			t.Fatal(err)
		}
		list, err := s.List()
		if err != nil {
			t.Fatal(err)
		}
		ids := map[string]bool{}
		for _, m := range list {
			ids[m.ID] = true
		}
		if ids["one"] || !ids["two"] || !ids["three"] {
			t.Errorf("after eviction = %v, want two+three", ids)
		}
	})
}

// TestConcurrentAccess runs the store under -race with parallel writers and
// readers over 100 sessions.
func TestConcurrentAccess(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(Options{Dir: dir, MaxSessions: 200, Summary: summarize})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := range 100 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("s%03d", i)
			at := time.Unix(int64(i), 0)
			p := payload{ID: id, Created: at, Action: at, Value: i}
			for range 3 {
				if err := s.Persist(metaFor(p), encode(p)); err != nil {
					t.Error(err)
					return
				}
				if _, _, err := s.Load(id); err != nil {
					t.Error(err)
					return
				}
				if _, err := s.List(); err != nil {
					t.Error(err)
					return
				}
			}
		}(i)
	}
	wg.Wait()
	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 100 {
		t.Errorf("List = %d entries, want 100", len(list))
	}
}

// TestPostgresBackend uses TEST_DATABASE_URL and a throwaway schema, skipped
// when the variable is unset.
func TestPostgresBackend(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	schema := fmt.Sprintf("store_%d", time.Now().UnixNano())

	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	s, err := Open(Options{Pool: pool, MaxSessions: 10, Summary: summarize})
	if err != nil {
		t.Fatal(err)
	}
	p := payload{ID: "pg1", Created: time.Unix(1, 0), Action: time.Unix(2, 0), Value: 9}
	if err := s.Persist(metaFor(p), encode(p)); err != nil {
		t.Fatal(err)
	}
	// Reopen against the same schema: disk row, not memory.
	s2, err := Open(Options{Pool: pool, Summary: summarize})
	if err != nil {
		t.Fatal(err)
	}
	list, err := s2.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != "pg1" || list[0].Loaded {
		t.Fatalf("List = %+v", list)
	}
	_, raw, err := s2.Load("pg1")
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != string(encode(p)) {
		t.Errorf("payload mismatch: %s", raw)
	}
	if err := s2.Delete("pg1"); err != nil {
		t.Fatal(err)
	}
}
