package account

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wricardo/drbrain-motor-programming/game/service"
	"github.com/wricardo/drbrain-motor-programming/internal/pgdb"
)

// PostgresStore implements service.AccountStore on the accounts and logins
// tables. The database enforces what the file store enforces in Go:
//
//   - handles are unique ignoring case (unique index on lower(handle));
//   - a provider identity belongs to one account (unique index);
//   - deleting an account removes its logins (ON DELETE CASCADE) and leaves its
//     sessions as guest rows (ON DELETE SET NULL on sessions.account_id);
//   - PutLogin takes the account's row lock, so two browsers signing in at once
//     cannot both slip past MaxLoginsPerAccount.
type PostgresStore struct {
	pool *pgxpool.Pool
}

var _ service.AccountStore = (*PostgresStore)(nil)

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

func scanAccount(row pgx.Row) (service.Account, error) {
	var a service.Account
	var changed *time.Time
	if err := row.Scan(&a.ID, &a.Provider, &a.ProviderUserID, &a.Handle, &a.DisplayName, &a.ProfilePublic,
		&a.CreatedAt, &changed, &a.PasswordHash); err != nil {
		return service.Account{}, err
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
		return service.ErrHandleTaken
	case identityIndex:
		return service.ErrIdentityExists
	}
	return err
}

// Create stores a new account. The handle and the provider identity must both be unused.
func (s *PostgresStore) Create(a service.Account) error {
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

func (s *PostgresStore) one(where string, args ...any) (service.Account, bool) {
	c, cancel := queryCtx()
	defer cancel()
	a, err := scanAccount(s.pool.QueryRow(c, `SELECT `+accountColumns+` FROM accounts WHERE `+where, args...))
	if err != nil {
		return service.Account{}, false
	}
	return a, true
}

// Get returns an account by id.
func (s *PostgresStore) Get(id string) (service.Account, bool) {
	if !idRE.MatchString(id) {
		return service.Account{}, false
	}
	return s.one(`id = $1`, id)
}

// ByIdentity returns the account of a provider identity.
func (s *PostgresStore) ByIdentity(provider, providerUserID string) (service.Account, bool) {
	return s.one(`provider = $1 AND provider_user_id = $2`, provider, providerUserID)
}

// ByHandle returns the account with the handle (compared case-insensitively).
func (s *PostgresStore) ByHandle(handle string) (service.Account, bool) {
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
func (s *PostgresStore) Update(id string, fn func(*service.Account) error) (service.Account, error) {
	c, cancel := queryCtx()
	defer cancel()
	tx, err := s.pool.Begin(c)
	if err != nil {
		return service.Account{}, fmt.Errorf("update account: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	cur, err := scanAccount(tx.QueryRow(c, `SELECT `+accountColumns+` FROM accounts WHERE id = $1 FOR UPDATE`, id))
	if err != nil {
		if pgdb.IsNoRows(err) {
			return service.Account{}, service.Errorf(service.CodeNotFound, "account not found")
		}
		return service.Account{}, fmt.Errorf("update account: %w", err)
	}
	next := cur
	if err := fn(&next); err != nil {
		return service.Account{}, err
	}
	next.ID, next.Provider, next.ProviderUserID, next.CreatedAt = cur.ID, cur.Provider, cur.ProviderUserID, cur.CreatedAt
	next.Handle = strings.ToLower(next.Handle)
	if !handleRE.MatchString(next.Handle) {
		return service.Account{}, errors.New("account: invalid handle")
	}
	if _, err := tx.Exec(c, `
UPDATE accounts SET handle = $2, display_name = $3, profile_public = $4, handle_changed_at = $5, password_hash = $6
WHERE id = $1`, id, next.Handle, next.DisplayName, next.ProfilePublic, nullTime(next.HandleChangedAt), next.PasswordHash); err != nil {
		if mapped := uniqueErr(err); mapped != err {
			return service.Account{}, mapped
		}
		return service.Account{}, fmt.Errorf("update account: %w", err)
	}
	if err := tx.Commit(c); err != nil {
		return service.Account{}, fmt.Errorf("update account: %w", err)
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
		return service.Errorf(service.CodeNotFound, "account not found")
	}
	return nil
}

// PutLogin stores a login and drops the account's oldest ones beyond
// service.MaxLoginsPerAccount, in one transaction under the account's row lock.
func (s *PostgresStore) PutLogin(l service.Login) error {
	if !hashRE.MatchString(l.TokenHash) {
		return ErrInvalidID
	}
	c, cancel := queryCtx()
	defer cancel()
	tx, err := s.pool.Begin(c)
	if err != nil {
		return fmt.Errorf("put login: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	// The lock serialises concurrent sign-ins of one account; it also proves the account exists.
	var locked string
	if err := tx.QueryRow(c, `SELECT id FROM accounts WHERE id = $1 FOR UPDATE`, l.AccountID).Scan(&locked); err != nil {
		if pgdb.IsNoRows(err) {
			return service.Errorf(service.CodeNotFound, "account not found")
		}
		return fmt.Errorf("put login: %w", err)
	}
	if _, err := tx.Exec(c, `
INSERT INTO logins (token_hash, account_id, created_at, expires_at) VALUES ($1, $2, $3, $4)
ON CONFLICT (token_hash) DO UPDATE SET account_id = EXCLUDED.account_id, created_at = EXCLUDED.created_at, expires_at = EXCLUDED.expires_at`,
		l.TokenHash, l.AccountID, l.CreatedAt, l.ExpiresAt); err != nil {
		return fmt.Errorf("put login: %w", err)
	}
	if _, err := tx.Exec(c, `
DELETE FROM logins WHERE account_id = $1 AND token_hash NOT IN (
    SELECT token_hash FROM logins WHERE account_id = $1 ORDER BY created_at DESC, token_hash DESC LIMIT $2)`,
		l.AccountID, service.MaxLoginsPerAccount); err != nil {
		return fmt.Errorf("put login: %w", err)
	}
	if err := tx.Commit(c); err != nil {
		return fmt.Errorf("put login: %w", err)
	}
	return nil
}

// GetLogin returns a login by token hash.
func (s *PostgresStore) GetLogin(tokenHash string) (service.Login, bool) {
	if !hashRE.MatchString(tokenHash) {
		return service.Login{}, false
	}
	c, cancel := queryCtx()
	defer cancel()
	var l service.Login
	err := s.pool.QueryRow(c, `SELECT token_hash, account_id, created_at, expires_at FROM logins WHERE token_hash = $1`, tokenHash).
		Scan(&l.TokenHash, &l.AccountID, &l.CreatedAt, &l.ExpiresAt)
	if err != nil {
		return service.Login{}, false
	}
	l.CreatedAt, l.ExpiresAt = l.CreatedAt.UTC(), l.ExpiresAt.UTC()
	return l, true
}

// DeleteOtherLogins removes every login of the account except keepHash (pass "" to remove them all).
func (s *PostgresStore) DeleteOtherLogins(accountID, keepHash string) error {
	if !idRE.MatchString(accountID) || (keepHash != "" && !hashRE.MatchString(keepHash)) {
		return ErrInvalidID
	}
	c, cancel := queryCtx()
	defer cancel()
	if _, err := s.pool.Exec(c, `DELETE FROM logins WHERE account_id = $1 AND token_hash <> $2`, accountID, keepHash); err != nil {
		return fmt.Errorf("remove logins: %w", err)
	}
	return nil
}

// DeleteLogin removes one login; unknown hashes are ignored.
func (s *PostgresStore) DeleteLogin(tokenHash string) error {
	if !hashRE.MatchString(tokenHash) {
		return ErrInvalidID
	}
	c, cancel := queryCtx()
	defer cancel()
	if _, err := s.pool.Exec(c, `DELETE FROM logins WHERE token_hash = $1`, tokenHash); err != nil {
		return fmt.Errorf("remove login: %w", err)
	}
	return nil
}

// SweepLogins deletes every login that has expired and returns how many it removed.
func (s *PostgresStore) SweepLogins(now time.Time) (int, error) {
	c, cancel := queryCtx()
	defer cancel()
	tag, err := s.pool.Exec(c, `DELETE FROM logins WHERE expires_at <= $1`, now)
	if err != nil {
		return 0, fmt.Errorf("sweep logins: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// Accounts returns every account (with password hashes), oldest first. It is
// for the JSON export; the service never calls it.
func (s *PostgresStore) Accounts() ([]service.Account, error) {
	c, cancel := queryCtx()
	defer cancel()
	rows, err := s.pool.Query(c, `SELECT `+accountColumns+` FROM accounts ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}
	defer rows.Close()
	var out []service.Account
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, fmt.Errorf("list accounts: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Logins returns every unexpired login, oldest first (for the JSON export).
func (s *PostgresStore) Logins(now time.Time) ([]service.Login, error) {
	c, cancel := queryCtx()
	defer cancel()
	rows, err := s.pool.Query(c, `SELECT token_hash, account_id, created_at, expires_at FROM logins WHERE expires_at > $1 ORDER BY created_at, token_hash`, now)
	if err != nil {
		return nil, fmt.Errorf("list logins: %w", err)
	}
	defer rows.Close()
	var out []service.Login
	for rows.Next() {
		var l service.Login
		if err := rows.Scan(&l.TokenHash, &l.AccountID, &l.CreatedAt, &l.ExpiresAt); err != nil {
			return nil, fmt.Errorf("list logins: %w", err)
		}
		l.CreatedAt, l.ExpiresAt = l.CreatedAt.UTC(), l.ExpiresAt.UTC()
		out = append(out, l)
	}
	return out, rows.Err()
}
