package pgdb_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wricardo/drbrain/platform/pgdb"
	"github.com/wricardo/drbrain/platform/pgdb/pgtest"
)

// An unreachable database is a clear startup error that names DATABASE_URL and
// never prints the password.
func TestOpenUnreachableNamesDatabaseURLAndHidesPassword(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_, err := pgdb.Open(ctx, "postgres://drbrain:hunter2-secret@127.0.0.1:1/drbrain?sslmode=disable&connect_timeout=2")
	if err == nil {
		t.Fatal("connected to a closed port")
	}
	if !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Errorf("error does not name DATABASE_URL: %v", err)
	}
	if strings.Contains(err.Error(), "hunter2-secret") {
		t.Errorf("error leaks the password: %v", err)
	}
}

func TestOpenRejectsEmptyAndMalformedURLs(t *testing.T) {
	for _, url := range []string{"", "   ", "postgres://user:p%zzw@host/db"} {
		_, err := pgdb.Open(context.Background(), url)
		if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
			t.Errorf("Open(%q) = %v, want an error naming DATABASE_URL", url, err)
		}
	}
}

func TestMigrateIsIdempotentAndRecordsVersions(t *testing.T) {
	pool := pgtest.Pool(t) // already migrated once
	ctx := context.Background()
	for range 2 {
		if err := pgdb.Migrate(ctx, pool); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&n); err != nil || n < 1 {
		t.Fatalf("schema_migrations rows = %d, %v", n, err)
	}
	for _, table := range []string{"sessions", "accounts", "logins", "maps"} {
		if _, err := pool.Exec(ctx, `SELECT 1 FROM `+table+` LIMIT 1`); err != nil {
			t.Errorf("table %s missing: %v", table, err)
		}
	}
}

// Two processes starting at once must not both run a migration.
func TestMigrateConcurrentStartsApplyOnce(t *testing.T) {
	pool := pgtest.Pool(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `DROP TABLE sessions, logins, accounts, maps, schema_migrations`); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := pgdb.Migrate(ctx, pool); err != nil {
				t.Errorf("Migrate: %v", err)
			}
		}()
	}
	wg.Wait()
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations WHERE version = 1`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("version 1 recorded %d times, %v", n, err)
	}
}

func TestSchemaEnforcesHandleAndIdentityUniqueness(t *testing.T) {
	pool := pgtest.Pool(t)
	ctx := context.Background()
	ins := `INSERT INTO accounts (id, provider, provider_user_id, handle, display_name, created_at) VALUES ($1, $2, $3, $4, $4, now())`
	if _, err := pool.Exec(ctx, ins, "a1", "google", "u1", "Otter"); err != nil {
		t.Fatal(err)
	}
	_, err := pool.Exec(ctx, ins, "a2", "github", "u2", "oTTER")
	if pgdb.Constraint(err) != "accounts_handle_key" {
		t.Errorf("case-different handle: constraint %q (%v)", pgdb.Constraint(err), err)
	}
	_, err = pool.Exec(ctx, ins, "a3", "google", "u1", "other")
	if pgdb.Constraint(err) != "accounts_identity_key" {
		t.Errorf("same identity: constraint %q (%v)", pgdb.Constraint(err), err)
	}
}
