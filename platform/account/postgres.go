package account

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wricardo/drbrain/platform/errs"
	"github.com/wricardo/drbrain/platform/pgdb"
)

// PostgresStore implements Repository on the accounts table. The database
// enforces what the file store enforces in Go:
//
//   - handles are unique ignoring case (unique index on lower(handle));
//   - a provider identity belongs to one account (unique index);
//   - deleting an account leaves its sessions as guest rows (ON DELETE SET NULL
//     on sessions.account_id).
type PostgresStore struct {
	pool *pgxpool.Pool
}

var _ Repository = (*PostgresStore)(nil)

// NewPostgresStore uses an already opened and migrated pool (pgdb.Open).
func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore { return &PostgresStore{pool: pool} }

// Index names from migrations/001_init.sql.
const (
	handleIndex   = "accounts_handle_key"
	identityIndex = "accounts_identity_key"
)

func queryCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), pgdb.QueryTimeout)
}

const accountColumns = `id, provider, provider_user_id, handle, display_name, profile_public,
       created_at, handle_changed_at, password_hash`

func scanAccount(row pgx.Row) (Account, error) {
	var a Account
	var changed *time.Time
	if err := row.Scan(&a.ID, &a.Provider, &a.ProviderUserID, &a.Handle, &a.DisplayName, &a.ProfilePublic,
		&a.CreatedAt, &changed, &a.PasswordHash); err != nil {
		return Account{}, err
	}
	// The driver returns local time; the file format and the API use UTC.
	a.CreatedAt = a.CreatedAt.UTC()
	if changed != nil {
		a.HandleChangedAt = changed.UTC()
	}
	if strings.TrimSpace(a.DisplayName) == "" {
		a.DisplayName = a.Handle
	}
	return a, nil
}

// nullTime stores the zero time as NULL.
func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

// uniqueErr maps a unique violation to the service's sentinel errors.
func uniqueErr(err error) error {
	switch pgdb.Constraint(err) {
	case handleIndex:
		return ErrHandleTaken
	case identityIndex:
		return ErrIdentityExists
	}
	return err
}

// Create stores a new account. The handle and the provider identity must both be unused.
func (s *PostgresStore) Create(a Account) error {
	if !idRE.MatchString(a.ID) {
		return ErrInvalidID
	}
	a.Handle = strings.ToLower(a.Handle)
	if !handleRE.MatchString(a.Handle) || !providerRE.MatchString(a.Provider) || a.ProviderUserID == "" {
		return errors.New("account: invalid handle or identity")
	}
	c, cancel := queryCtx()
	defer cancel()
	_, err := s.pool.Exec(c, `
INSERT INTO accounts (id, provider, provider_user_id, handle, display_name, profile_public, created_at, handle_changed_at, password_hash)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		a.ID, a.Provider, a.ProviderUserID, a.Handle, a.DisplayName, a.ProfilePublic, a.CreatedAt, nullTime(a.HandleChangedAt), a.PasswordHash)
	if err != nil {
		if mapped := uniqueErr(err); mapped != err {
			return mapped
		}
		if pgdb.Constraint(err) != "" {
			return fmt.Errorf("account %s already exists", a.ID)
		}
		return fmt.Errorf("create account: %w", err)
	}
	return nil
}

func (s *PostgresStore) one(where string, args ...any) (Account, bool) {
	c, cancel := queryCtx()
	defer cancel()
	a, err := scanAccount(s.pool.QueryRow(c, `SELECT `+accountColumns+` FROM accounts WHERE `+where, args...))
	if err != nil {
		return Account{}, false
	}
	return a, true
}

// Get returns an account by id.
func (s *PostgresStore) Get(id string) (Account, bool) {
	if !idRE.MatchString(id) {
		return Account{}, false
	}
	return s.one(`id = $1`, id)
}

// ByIdentity returns the account of a provider identity.
func (s *PostgresStore) ByIdentity(provider, providerUserID string) (Account, bool) {
	return s.one(`provider = $1 AND provider_user_id = $2`, provider, providerUserID)
}

// ByHandle returns the account with the handle (compared case-insensitively).
func (s *PostgresStore) ByHandle(handle string) (Account, bool) {
	return s.one(`lower(handle) = $1`, strings.ToLower(handle))
}

// Count returns the number of accounts.
func (s *PostgresStore) Count() int {
	c, cancel := queryCtx()
	defer cancel()
	var n int
	if err := s.pool.QueryRow(c, `SELECT count(*) FROM accounts`).Scan(&n); err != nil {
		return 0
	}
	return n
}

// Update applies fn to a copy of the account and stores the result. The row is
// locked for the read-modify-write, so two updates of one account never lose
// each other's changes. The identity and id cannot be changed.
func (s *PostgresStore) Update(id string, fn func(*Account) error) (Account, error) {
	c, cancel := queryCtx()
	defer cancel()
	tx, err := s.pool.Begin(c)
	if err != nil {
		return Account{}, fmt.Errorf("update account: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	cur, err := scanAccount(tx.QueryRow(c, `SELECT `+accountColumns+` FROM accounts WHERE id = $1 FOR UPDATE`, id))
	if err != nil {
		if pgdb.IsNoRows(err) {
			return Account{}, errs.Errorf(errs.CodeNotFound, "account not found")
		}
		return Account{}, fmt.Errorf("update account: %w", err)
	}
	next := cur
	if err := fn(&next); err != nil {
		return Account{}, err
	}
	next.ID, next.Provider, next.ProviderUserID, next.CreatedAt = cur.ID, cur.Provider, cur.ProviderUserID, cur.CreatedAt
	next.Handle = strings.ToLower(next.Handle)
	if !handleRE.MatchString(next.Handle) {
		return Account{}, errors.New("account: invalid handle")
	}
	if _, err := tx.Exec(c, `
UPDATE accounts SET handle = $2, display_name = $3, profile_public = $4, handle_changed_at = $5, password_hash = $6
WHERE id = $1`, id, next.Handle, next.DisplayName, next.ProfilePublic, nullTime(next.HandleChangedAt), next.PasswordHash); err != nil {
		if mapped := uniqueErr(err); mapped != err {
			return Account{}, mapped
		}
		return Account{}, fmt.Errorf("update account: %w", err)
	}
	if err := tx.Commit(c); err != nil {
		return Account{}, fmt.Errorf("update account: %w", err)
	}
	return next, nil
}

// Delete removes the account; its logins go with it and its sessions become guest rows.
func (s *PostgresStore) Delete(id string) error {
	if !idRE.MatchString(id) {
		return ErrInvalidID
	}
	c, cancel := queryCtx()
	defer cancel()
	tag, err := s.pool.Exec(c, `DELETE FROM accounts WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete account: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return errs.Errorf(errs.CodeNotFound, "account not found")
	}
	return nil
}

// Accounts returns every account (with password hashes), oldest first. It is
// for the JSON export; the service never calls it.
func (s *PostgresStore) Accounts() ([]Account, error) {
	c, cancel := queryCtx()
	defer cancel()
	rows, err := s.pool.Query(c, `SELECT `+accountColumns+` FROM accounts ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}
	defer rows.Close()
	var out []Account
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, fmt.Errorf("list accounts: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
