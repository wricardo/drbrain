// Package pgdb opens the PostgreSQL connection pool and applies the embedded
// schema migrations. It is the only place that knows how the database is
// reached; the session, account and map stores take a *pgxpool.Pool.
package pgdb

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// migrationLockKey is the advisory-lock key that serialises migrations when
// two processes start at once.
const migrationLockKey int64 = 0x6472627261696e // "drbrain"

// QueryTimeout bounds one store call, so a stuck database cannot hold a session
// persist (and the lock order around it) forever.
const QueryTimeout = 10 * time.Second

// Open connects to the database named by databaseURL, checks it answers and
// applies any pending migrations. The error never contains the URL's password.
func Open(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	return OpenSchema(ctx, databaseURL, "")
}

// OpenSchema is Open with every connection's search_path set to schema (which
// must already exist). Tests use it to give each test its own empty copy of the
// tables; an empty schema means the database's default.
func OpenSchema(ctx context.Context, databaseURL, schema string) (*pgxpool.Pool, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return nil, errors.New("DATABASE_URL is empty")
	}
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("DATABASE_URL is not a valid connection string: %s", redactErr(err, databaseURL))
	}
	if schema != "" {
		cfg.ConnConfig.RuntimeParams["search_path"] = schema
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect to the database named by DATABASE_URL: %s", redactErr(err, databaseURL))
	}
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("database named by DATABASE_URL is unreachable: %s", redactErr(err, databaseURL))
	}
	if err := Migrate(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

// redactErr removes the password (and the whole URL) from an error message.
func redactErr(err error, databaseURL string) string {
	msg := err.Error()
	if u, perr := url.Parse(databaseURL); perr == nil && u.User != nil {
		if pw, ok := u.User.Password(); ok && pw != "" {
			msg = strings.ReplaceAll(msg, pw, "***")
		}
	}
	return strings.ReplaceAll(msg, databaseURL, "<DATABASE_URL>")
}

type migration struct {
	version int
	name    string
	sql     string
}

func loadMigrations() ([]migration, error) {
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return nil, err
	}
	var out []migration
	for _, e := range entries {
		var v int
		if _, err := fmt.Sscanf(e.Name(), "%d_", &v); err != nil || !strings.HasSuffix(e.Name(), ".sql") {
			return nil, fmt.Errorf("migration %q: name must be <number>_<name>.sql", e.Name())
		}
		body, err := migrationFiles.ReadFile("migrations/" + e.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, migration{version: v, name: e.Name(), sql: string(body)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

// Migrate applies every embedded migration that has not run yet, each in its
// own transaction, under an advisory lock so concurrent starts do not race.
// Applied versions are recorded in schema_migrations.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	migs, err := loadMigrations()
	if err != nil {
		return fmt.Errorf("load migrations: %w", err)
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrationLockKey); err != nil {
		return fmt.Errorf("migrate: take advisory lock: %w", err)
	}
	defer func() {
		// Best effort: the lock also ends with the connection.
		_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, migrationLockKey)
	}()

	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version integer PRIMARY KEY, name text NOT NULL, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return fmt.Errorf("migrate: create schema_migrations: %w", err)
	}
	for _, m := range migs {
		var done bool
		if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)`, m.version).Scan(&done); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
		if done {
			continue
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			return fmt.Errorf("migrate %s: %w", m.name, err)
		}
		if _, err := tx.Exec(ctx, m.sql); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("migrate %s: %w", m.name, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version, name) VALUES ($1, $2)`, m.version, m.name); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("migrate %s: %w", m.name, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("migrate %s: %w", m.name, err)
		}
	}
	return nil
}

// Constraint returns the name of the violated unique constraint or index when
// err is a unique violation (SQLSTATE 23505), and "" otherwise.
func Constraint(err error) string {
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23505" {
		return pe.ConstraintName
	}
	return ""
}

// IsForeignKeyViolation reports a SQLSTATE 23503 error.
func IsForeignKeyViolation(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23503"
}

// IsNoRows reports pgx.ErrNoRows.
func IsNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
