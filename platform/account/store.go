// Package account owns player accounts and logins for every game.
//
// The file-backed Store keeps the layout under the accounts directory:
// Layout under the accounts directory:
//
//	accounts/<id>.json     one account (id: 24 lower-case hex characters)
//	logins/<hash>.json     one login, named by the SHA-256 (64 hex) of its cookie token
//
// The lookup indexes (handle, provider identity, logins per account) are kept in
// memory and rebuilt from the files at startup, so there is no second copy on
// disk that could disagree. Every write is atomic (temp file in the same
// directory, fsync, rename). Ids and hashes are validated before they touch the
// file system. Only the minimum is stored: see Account.
package account

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/wricardo/drbrain/platform/errs"
)

// SchemaVersion is the on-disk format version of an account file.
const SchemaVersion = 1

const fileExt = ".json"

var (
	idRE   = regexp.MustCompile(`^[0-9a-f]{24}$`)
	hashRE = regexp.MustCompile(`^[0-9a-f]{64}$`)
	// providerRE keeps provider names safe to embed in an index key.
	providerRE = regexp.MustCompile(`^[a-z0-9_-]{1,32}$`)
)

// ErrInvalidID is returned for an account id or login hash of the wrong shape.
var ErrInvalidID = errors.New("account: invalid id")

// persistedAccount is the JSON form of an account.
type persistedAccount struct {
	SchemaVersion   int       `json:"schema_version"`
	ID              string    `json:"id"`
	Provider        string    `json:"provider"`
	ProviderUserID  string    `json:"provider_user_id"`
	Handle          string    `json:"handle"`
	DisplayName     string    `json:"display_name"`
	ProfilePublic   bool      `json:"profile_public"`
	CreatedAt       time.Time `json:"created_at"`
	HandleChangedAt time.Time `json:"handle_changed_at,omitempty"`
	// PasswordHash is the argon2id hash of a username/password account (never the password).
	PasswordHash string `json:"password_hash,omitempty"`
}

// Store implements Repository on the file system.
type Store struct {
	accountsDir string
	now         func() time.Time

	mu         sync.RWMutex
	accounts   map[string]Account // id -> account
	byHandle   map[string]string  // lower-case handle -> id
	byIdentity map[string]string  // provider + "\x00" + user id -> id
}

var _ Repository = (*Store)(nil)

// Open creates the directory layout if needed and loads every account. Corrupt
// files are logged and skipped. now may be nil.
func Open(dir string, now func() time.Time) (*Store, error) {
	if now == nil {
		now = time.Now
	}
	s := &Store{
		accountsDir: filepath.Join(dir, "accounts"),
		now:         now,
		accounts:    make(map[string]Account),
		byHandle:    make(map[string]string),
		byIdentity:  make(map[string]string),
	}
	for _, d := range []string{s.accountsDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, fmt.Errorf("create accounts directory: %w", err)
		}
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func identityKey(provider, userID string) string { return provider + "\x00" + userID }

func (s *Store) accountPath(id string) string { return filepath.Join(s.accountsDir, id+fileExt) }

func (s *Store) load() error {
	entries, err := os.ReadDir(s.accountsDir)
	if err != nil {
		return fmt.Errorf("read accounts directory: %w", err)
	}
	for _, e := range entries {
		id := strings.TrimSuffix(e.Name(), fileExt)
		if e.IsDir() || !strings.HasSuffix(e.Name(), fileExt) || !idRE.MatchString(id) {
			continue
		}
		a, err := s.readAccount(id)
		if err != nil {
			log.Printf("account: skipping %s: %v", id, err)
			continue
		}
		if other, dup := s.byHandle[a.Handle]; dup {
			log.Printf("account: skipping %s: handle %q already used by %s", id, a.Handle, other)
			continue
		}
		if other, dup := s.byIdentity[identityKey(a.Provider, a.ProviderUserID)]; dup {
			log.Printf("account: skipping %s: sign-in identity already used by %s", id, other)
			continue
		}
		s.index(a)
	}

	return nil
}

func (s *Store) readAccount(id string) (Account, error) {
	raw, err := os.ReadFile(s.accountPath(id))
	if err != nil {
		return Account{}, err
	}
	var pa persistedAccount
	if err := json.Unmarshal(raw, &pa); err != nil {
		return Account{}, err
	}
	switch {
	case pa.SchemaVersion != SchemaVersion:
		return Account{}, fmt.Errorf("unsupported schema_version %d", pa.SchemaVersion)
	case pa.ID != id:
		return Account{}, fmt.Errorf("file contains id %q", pa.ID)
	case !handleRE.MatchString(pa.Handle):
		return Account{}, fmt.Errorf("invalid handle %q", pa.Handle)
	case !providerRE.MatchString(pa.Provider) || pa.ProviderUserID == "":
		return Account{}, errors.New("invalid sign-in identity")
	}
	if strings.TrimSpace(pa.DisplayName) == "" {
		pa.DisplayName = pa.Handle
	}
	return Account{
		ID: pa.ID, Provider: pa.Provider, ProviderUserID: pa.ProviderUserID, Handle: pa.Handle,
		DisplayName: pa.DisplayName, ProfilePublic: pa.ProfilePublic,
		CreatedAt: pa.CreatedAt, HandleChangedAt: pa.HandleChangedAt, PasswordHash: pa.PasswordHash,
	}, nil
}

// index adds a to the in-memory maps. Caller holds the write lock (or is load).
func (s *Store) index(a Account) {
	s.accounts[a.ID] = a
	s.byHandle[a.Handle] = a.ID
	s.byIdentity[identityKey(a.Provider, a.ProviderUserID)] = a.ID
}

func (s *Store) writeAccount(a Account) error {
	return writeJSON(s.accountsDir, a.ID, persistedAccount{
		SchemaVersion: SchemaVersion, ID: a.ID, Provider: a.Provider, ProviderUserID: a.ProviderUserID,
		Handle: a.Handle, DisplayName: a.DisplayName, ProfilePublic: a.ProfilePublic,
		CreatedAt: a.CreatedAt, HandleChangedAt: a.HandleChangedAt, PasswordHash: a.PasswordHash,
	})
}

// Create stores a new account. The handle and the provider identity must both
// be unused.
func (s *Store) Create(a Account) error {
	if !idRE.MatchString(a.ID) {
		return ErrInvalidID
	}
	a.Handle = strings.ToLower(a.Handle)
	if !handleRE.MatchString(a.Handle) || !providerRE.MatchString(a.Provider) || a.ProviderUserID == "" {
		return errors.New("account: invalid handle or identity")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.accounts[a.ID]; ok {
		return fmt.Errorf("account %s already exists", a.ID)
	}
	if _, ok := s.byIdentity[identityKey(a.Provider, a.ProviderUserID)]; ok {
		return ErrIdentityExists
	}
	if _, ok := s.byHandle[a.Handle]; ok {
		return ErrHandleTaken
	}
	if err := s.writeAccount(a); err != nil {
		return err
	}
	s.index(a)
	return nil
}

// Get returns an account by id.
func (s *Store) Get(id string) (Account, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.accounts[id]
	return a, ok
}

// ByIdentity returns the account of a provider identity.
func (s *Store) ByIdentity(provider, providerUserID string) (Account, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.byIdentity[identityKey(provider, providerUserID)]
	if !ok {
		return Account{}, false
	}
	return s.accounts[id], true
}

// ByHandle returns the account with the handle (compared case-insensitively).
func (s *Store) ByHandle(handle string) (Account, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.byHandle[strings.ToLower(handle)]
	if !ok {
		return Account{}, false
	}
	return s.accounts[id], true
}

// Count returns the number of accounts.
func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.accounts)
}

// Update applies fn to a copy of the account and stores the result. The
// identity and id cannot be changed; a handle change must be free.
func (s *Store) Update(id string, fn func(*Account) error) (Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.accounts[id]
	if !ok {
		return Account{}, errs.Errorf(errs.CodeNotFound, "account not found")
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
	if next.Handle != cur.Handle {
		if other, taken := s.byHandle[next.Handle]; taken && other != id {
			return Account{}, ErrHandleTaken
		}
	}
	if err := s.writeAccount(next); err != nil {
		return Account{}, err
	}
	if next.Handle != cur.Handle {
		delete(s.byHandle, cur.Handle)
	}
	s.index(next)
	return next, nil
}

// Delete removes the account file and its index entries.
func (s *Store) Delete(id string) error {
	if !idRE.MatchString(id) {
		return ErrInvalidID
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.accounts[id]
	if !ok {
		return errs.Errorf(errs.CodeNotFound, "account not found")
	}
	if err := os.Remove(s.accountPath(id)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove account: %w", err)
	}
	if err := syncDir(s.accountsDir); err != nil {
		return err
	}
	delete(s.accounts, id)
	delete(s.byHandle, a.Handle)
	delete(s.byIdentity, identityKey(a.Provider, a.ProviderUserID))
	return nil
}

// writeJSON atomically writes v to <dir>/<name>.json: a temp file in the same
// directory (its name never ends in .json, so a partial write is never read
// back), fsync, rename, then a directory sync where the platform supports it.
func writeJSON(dir, name string, v any) (err error) {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", name, err)
	}
	tmp, err := os.CreateTemp(dir, ".tmp-"+name+"-*")
	if err != nil {
		return fmt.Errorf("create temp file for %s: %w", name, err)
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}
	if err = tmp.Sync(); err != nil {
		return fmt.Errorf("sync %s: %w", name, err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", name, err)
	}
	if err = os.Rename(tmp.Name(), filepath.Join(dir, name+fileExt)); err != nil {
		return fmt.Errorf("replace %s: %w", name, err)
	}
	return syncDir(dir)
}

// syncDir fsyncs a directory so renames and removals are durable. Windows cannot
// open a directory for syncing, and there the call is skipped.
func syncDir(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	if err := d.Sync(); err != nil {
		_ = d.Close()
		return err
	}
	return d.Close()
}
