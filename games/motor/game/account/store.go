// Package account is the file-backed implementation of service.AccountStore.
//
// Layout under the accounts directory:
//
//	accounts/<id>.json     one account (id: 24 lower-case hex characters)
//	logins/<hash>.json     one login, named by the SHA-256 (64 hex) of its cookie token
//
// The lookup indexes (handle, provider identity, logins per account) are kept in
// memory and rebuilt from the files at startup, so there is no second copy on
// disk that could disagree. Every write is atomic (temp file in the same
// directory, fsync, rename). Ids and hashes are validated before they touch the
// file system. Only the minimum is stored: see service.Account.
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
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/wricardo/drbrain-motor-programming/game/service"
)

// SchemaVersion is the on-disk format version of an account file.
const SchemaVersion = 1

const fileExt = ".json"

var (
	idRE     = regexp.MustCompile(`^[0-9a-f]{24}$`)
	hashRE   = regexp.MustCompile(`^[0-9a-f]{64}$`)
	handleRE = regexp.MustCompile(`^[a-z0-9_]{3,20}$`)
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
}

// persistedLogin is the JSON form of a login (the file name is its token hash).
type persistedLogin struct {
	SchemaVersion int       `json:"schema_version"`
	AccountID     string    `json:"account_id"`
	CreatedAt     time.Time `json:"created_at"`
	ExpiresAt     time.Time `json:"expires_at"`
}

// Store implements service.AccountStore on the file system.
type Store struct {
	accountsDir string
	loginsDir   string
	now         func() time.Time

	mu         sync.RWMutex
	accounts   map[string]service.Account // id -> account
	byHandle   map[string]string          // lower-case handle -> id
	byIdentity map[string]string          // provider + "\x00" + user id -> id
	logins     map[string]service.Login   // token hash -> login
	byAccount  map[string][]string        // account id -> login hashes, oldest first
}

var _ service.AccountStore = (*Store)(nil)

// Open creates the directory layout if needed and loads every account and
// unexpired login. Corrupt files are logged and skipped. now may be nil.
func Open(dir string, now func() time.Time) (*Store, error) {
	if now == nil {
		now = time.Now
	}
	s := &Store{
		accountsDir: filepath.Join(dir, "accounts"),
		loginsDir:   filepath.Join(dir, "logins"),
		now:         now,
		accounts:    make(map[string]service.Account),
		byHandle:    make(map[string]string),
		byIdentity:  make(map[string]string),
		logins:      make(map[string]service.Login),
		byAccount:   make(map[string][]string),
	}
	for _, d := range []string{s.accountsDir, s.loginsDir} {
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
func (s *Store) loginPath(h string) string    { return filepath.Join(s.loginsDir, h+fileExt) }

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

	entries, err = os.ReadDir(s.loginsDir)
	if err != nil {
		return fmt.Errorf("read logins directory: %w", err)
	}
	now := s.now()
	var logins []service.Login
	for _, e := range entries {
		h := strings.TrimSuffix(e.Name(), fileExt)
		if e.IsDir() || !strings.HasSuffix(e.Name(), fileExt) || !hashRE.MatchString(h) {
			continue
		}
		raw, err := os.ReadFile(s.loginPath(h))
		var pl persistedLogin
		if err == nil {
			err = json.Unmarshal(raw, &pl)
		}
		_, known := s.accounts[pl.AccountID]
		if err != nil || pl.SchemaVersion != SchemaVersion || !known || !now.Before(pl.ExpiresAt) {
			_ = os.Remove(s.loginPath(h)) // expired, orphaned or unreadable: nothing to keep
			continue
		}
		logins = append(logins, service.Login{TokenHash: h, AccountID: pl.AccountID, CreatedAt: pl.CreatedAt, ExpiresAt: pl.ExpiresAt})
	}
	sort.Slice(logins, func(i, j int) bool { return logins[i].CreatedAt.Before(logins[j].CreatedAt) })
	for _, l := range logins {
		s.logins[l.TokenHash] = l
		s.byAccount[l.AccountID] = append(s.byAccount[l.AccountID], l.TokenHash)
	}
	return nil
}

func (s *Store) readAccount(id string) (service.Account, error) {
	raw, err := os.ReadFile(s.accountPath(id))
	if err != nil {
		return service.Account{}, err
	}
	var pa persistedAccount
	if err := json.Unmarshal(raw, &pa); err != nil {
		return service.Account{}, err
	}
	switch {
	case pa.SchemaVersion != SchemaVersion:
		return service.Account{}, fmt.Errorf("unsupported schema_version %d", pa.SchemaVersion)
	case pa.ID != id:
		return service.Account{}, fmt.Errorf("file contains id %q", pa.ID)
	case !handleRE.MatchString(pa.Handle):
		return service.Account{}, fmt.Errorf("invalid handle %q", pa.Handle)
	case !providerRE.MatchString(pa.Provider) || pa.ProviderUserID == "":
		return service.Account{}, errors.New("invalid sign-in identity")
	}
	if strings.TrimSpace(pa.DisplayName) == "" {
		pa.DisplayName = pa.Handle
	}
	return service.Account{
		ID: pa.ID, Provider: pa.Provider, ProviderUserID: pa.ProviderUserID, Handle: pa.Handle,
		DisplayName: pa.DisplayName, ProfilePublic: pa.ProfilePublic,
		CreatedAt: pa.CreatedAt, HandleChangedAt: pa.HandleChangedAt,
	}, nil
}

// index adds a to the in-memory maps. Caller holds the write lock (or is load).
func (s *Store) index(a service.Account) {
	s.accounts[a.ID] = a
	s.byHandle[a.Handle] = a.ID
	s.byIdentity[identityKey(a.Provider, a.ProviderUserID)] = a.ID
}

func (s *Store) writeAccount(a service.Account) error {
	return writeJSON(s.accountsDir, a.ID, persistedAccount{
		SchemaVersion: SchemaVersion, ID: a.ID, Provider: a.Provider, ProviderUserID: a.ProviderUserID,
		Handle: a.Handle, DisplayName: a.DisplayName, ProfilePublic: a.ProfilePublic,
		CreatedAt: a.CreatedAt, HandleChangedAt: a.HandleChangedAt,
	})
}

// Create stores a new account. The handle and the provider identity must both
// be unused.
func (s *Store) Create(a service.Account) error {
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
		return service.ErrIdentityExists
	}
	if _, ok := s.byHandle[a.Handle]; ok {
		return service.ErrHandleTaken
	}
	if err := s.writeAccount(a); err != nil {
		return err
	}
	s.index(a)
	return nil
}

// Get returns an account by id.
func (s *Store) Get(id string) (service.Account, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.accounts[id]
	return a, ok
}

// ByIdentity returns the account of a provider identity.
func (s *Store) ByIdentity(provider, providerUserID string) (service.Account, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.byIdentity[identityKey(provider, providerUserID)]
	if !ok {
		return service.Account{}, false
	}
	return s.accounts[id], true
}

// ByHandle returns the account with the handle (compared case-insensitively).
func (s *Store) ByHandle(handle string) (service.Account, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.byHandle[strings.ToLower(handle)]
	if !ok {
		return service.Account{}, false
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
func (s *Store) Update(id string, fn func(*service.Account) error) (service.Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.accounts[id]
	if !ok {
		return service.Account{}, service.Errorf(service.CodeNotFound, "account not found")
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
	if next.Handle != cur.Handle {
		if other, taken := s.byHandle[next.Handle]; taken && other != id {
			return service.Account{}, service.ErrHandleTaken
		}
	}
	if err := s.writeAccount(next); err != nil {
		return service.Account{}, err
	}
	if next.Handle != cur.Handle {
		delete(s.byHandle, cur.Handle)
	}
	s.index(next)
	return next, nil
}

// Delete removes the account file, its logins and its index entries.
func (s *Store) Delete(id string) error {
	if !idRE.MatchString(id) {
		return ErrInvalidID
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.accounts[id]
	if !ok {
		return service.Errorf(service.CodeNotFound, "account not found")
	}
	// Logins first: with the account file gone they could never match again,
	// but nothing sensitive should linger after a delete.
	for _, h := range s.byAccount[id] {
		if err := os.Remove(s.loginPath(h)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove login: %w", err)
		}
		delete(s.logins, h)
	}
	delete(s.byAccount, id)
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

// PutLogin stores a login and drops the account's oldest ones beyond
// service.MaxLoginsPerAccount.
func (s *Store) PutLogin(l service.Login) error {
	if !hashRE.MatchString(l.TokenHash) {
		return ErrInvalidID
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.accounts[l.AccountID]; !ok {
		return service.Errorf(service.CodeNotFound, "account not found")
	}
	if err := writeJSON(s.loginsDir, l.TokenHash, persistedLogin{
		SchemaVersion: SchemaVersion, AccountID: l.AccountID, CreatedAt: l.CreatedAt, ExpiresAt: l.ExpiresAt,
	}); err != nil {
		return err
	}
	s.logins[l.TokenHash] = l
	s.byAccount[l.AccountID] = append(s.byAccount[l.AccountID], l.TokenHash)
	for hs := s.byAccount[l.AccountID]; len(hs) > service.MaxLoginsPerAccount; hs = s.byAccount[l.AccountID] {
		old := hs[0]
		s.byAccount[l.AccountID] = hs[1:]
		delete(s.logins, old)
		_ = os.Remove(s.loginPath(old))
	}
	return nil
}

// GetLogin returns a login by token hash.
func (s *Store) GetLogin(tokenHash string) (service.Login, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	l, ok := s.logins[tokenHash]
	return l, ok
}

// DeleteLogin removes one login; unknown hashes are ignored.
func (s *Store) DeleteLogin(tokenHash string) error {
	if !hashRE.MatchString(tokenHash) {
		return ErrInvalidID
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.logins[tokenHash]
	if !ok {
		return nil
	}
	delete(s.logins, tokenHash)
	hs := s.byAccount[l.AccountID]
	for i, h := range hs {
		if h == tokenHash {
			s.byAccount[l.AccountID] = append(hs[:i:i], hs[i+1:]...)
			break
		}
	}
	if err := os.Remove(s.loginPath(tokenHash)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove login: %w", err)
	}
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
