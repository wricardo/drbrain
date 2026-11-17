// Package pgtest gives database-backed tests an empty, migrated database.
//
// Tests that need PostgreSQL call Pool; it skips the test when TEST_DATABASE_URL
// is unset, so `go test ./...` needs no database. Every call creates its own
// schema (and drops it afterwards), so packages and tests that run in parallel
// never see each other's rows.
package pgtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wricardo/drbrain-motor-programming/internal/pgdb"
)

// Pool returns a pool on a fresh, migrated schema of the database named by
// TEST_DATABASE_URL, or skips the test when that variable is unset.
func Pool(t testing.TB) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set; skipping the PostgreSQL test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	schema := "t_" + hex.EncodeToString(b[:])

	admin, err := pgdb.Open(ctx, url)
	if err != nil {
		t.Fatalf("connect to TEST_DATABASE_URL: %v", err)
	}
	if _, err := admin.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		admin.Close()
		t.Fatalf("create test schema: %v", err)
	}
	pool, err := pgdb.OpenSchema(ctx, url, schema)
	if err != nil {
		_, _ = admin.Exec(ctx, `DROP SCHEMA `+schema+` CASCADE`)
		admin.Close()
		t.Fatalf("open test schema: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = admin.Exec(c, `DROP SCHEMA `+schema+` CASCADE`)
		admin.Close()
	})
	return pool
}
