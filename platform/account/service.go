// Package account owns player accounts and logins for every game: the model,
// the file and PostgreSQL stores, password hashing, handles and the account
// service the GraphQL API and auth flows call.
package account

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/wricardo/drbrain/platform/errs"
)

// Account limits and defaults.
const (
	// DefaultLoginTTL is how long a login lasts. It is fixed, not sliding: the
	// player signs in again after 30 days.
	DefaultLoginTTL = 30 * 24 * time.Hour
	// DefaultHandleCooldown is the minimum time between two handle changes. The
	// first change after sign-up (the handle was generated) is free.
	DefaultHandleCooldown = 24 * time.Hour
	// DefaultMaxAccounts caps how many accounts exist.
	DefaultMaxAccounts = 10000
	// MaxLoginsPerAccount is how many browsers may be signed in at once; the
	// oldest login is dropped beyond it.
	MaxLoginsPerAccount = 10
	// PrivatePlayerName is shown on boards for an account whose profile is private.
	PrivatePlayerName = "Private player"

	tokenBytes = 32
)

// CodeUnauthenticated is the extensions.code for operations that need a signed-in player.
const CodeUnauthenticated = errs.CodeUnauthenticated

// Sentinel errors a store returns so the service can retry or report them.
var (
	ErrHandleTaken    = errs.Errorf(errs.CodeInvalidArgument, "that handle is already taken")
	ErrIdentityExists = errs.Errorf(errs.CodeInvalidArgument, "that sign-in identity already has an account")
)

// Account is a player who signed in. It deliberately holds nothing about the
// person beyond the provider's opaque, stable user id (used only to recognise
// them on the next sign-in): no email address, no avatar, no provider profile.
type Account struct {
	ID string
	// Provider and ProviderUserID identify the sign-in ("google", "104...").
	// They are never exposed through the API.
	Provider       string
	ProviderUserID string
	// Handle is the unique public name (lower case, NormalizeHandle/ValidateHandle).
	Handle string
	// DisplayName is the public friendly name; never empty (defaults to the handle).
	DisplayName string
	// ProfilePublic false hides the profile page and masks board entries.
	ProfilePublic bool
	CreatedAt     time.Time
	// HandleChangedAt is when the player last chose a handle; zero until the
	// first change (the generated handle is free to replace once).
	HandleChangedAt time.Time
	// PasswordHash is the argon2id hash of a username/password account's
	// password ("" for Google/GitHub accounts). It exists only inside the store
	// and password code: every Account the service hands out has it cleared and
	// HasPassword set instead.
	PasswordHash string
	// HasPassword reports that the account signs in with a password.
	HasPassword bool
}

// publicAccount is a's copy that is safe to pass around: no password hash.
func publicAccount(a Account) Account {
	a.HasPassword = a.PasswordHash != ""
	a.PasswordHash = ""
	return a
}

// Login is one signed-in browser. Only the SHA-256 of the cookie token is
// stored, so a copy of the data directory cannot be used to sign in.
type Login struct {
	TokenHash string // hex SHA-256 of the token
	AccountID string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// Store persists accounts and logins. Create enforces unique handles
// (case-insensitive) and unique provider identities atomically; Update does the
// same when the handle changes.
type Repository interface {
	Create(a Account) error // ErrHandleTaken, ErrIdentityExists
	Get(id string) (Account, bool)
	ByIdentity(provider, providerUserID string) (Account, bool)
	ByHandle(handle string) (Account, bool)
	// Update applies fn to a copy of the account and stores the result if fn
	// returns nil. Changing the handle to one held by another account is ErrHandleTaken.
	Update(id string, fn func(*Account) error) (Account, error)
	// Delete removes the account and every login it owns.
	Delete(id string) error
	// DeleteOtherLogins removes every login of the account except keepHash ("" keeps none).
	DeleteOtherLogins(accountID, keepHash string) error
	Count() int

	// PutLogin stores a login, dropping the account's oldest ones beyond MaxLoginsPerAccount.
	PutLogin(l Login) error
	GetLogin(tokenHash string) (Login, bool)
	DeleteLogin(tokenHash string) error
}

// AccountUpdate is the set of changes a player may make; nil fields are kept.
type AccountUpdate struct {
	Handle        *string
	DisplayName   *string
	ProfilePublic *bool
}

// Profile is a player's public page: the account identity. Game-specific
// statistics live with each game.
type Profile struct {
	Account Account
	// IsMine is true when the viewer is this account.
	IsMine bool
}

// Options configures a Service.
type Options struct {
	Store          Repository
	PasswordSignIn bool
	LoginTTL       time.Duration
	HandleCooldown time.Duration
	MaxAccounts    int
	MaxDisplayName int
	Now            func() time.Time
	// OnDelete runs before the account row disappears so a game can detach
	// sessions and other data. Returning an error aborts the deletion.
	OnDelete func(accountID string) error
}

// Service is the account and login service shared by every game.
type Service struct {
	opts  Options
	store Repository
	mu    sync.RWMutex
	onDel func(string) error
}

// New returns the account service. A nil store keeps sign-in disabled: every
// mutating method reports FORBIDDEN and lookups return not-found.
func New(opts Options) *Service {
	return &Service{opts: opts, store: opts.Store, onDel: opts.OnDelete}
}

// SetOnDelete installs the game hook that detaches data before an account row
// disappears. Later calls replace it.
func (s *Service) SetOnDelete(fn func(string) error) {
	s.mu.Lock()
	s.onDel = fn
	s.mu.Unlock()
}

func (s *Service) deleteHook() func(string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.onDel
}

// Enabled reports whether accounts are configured.
func (s *Service) Enabled() bool { return s.store != nil }

// Store exposes the underlying store for game-specific lookups.

func (s *Service) now() time.Time {
	if s.opts.Now != nil {
		return s.opts.Now()
	}
	return time.Now()
}

// newToken returns a random URL-safe token and its storage hash.
func newToken() (token, hash string, err error) {
	var b [tokenBytes]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", "", fmt.Errorf("generate token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(b[:])
	return token, HashToken(token), nil
}

// HashToken is the storage form of a token: lower-case hex SHA-256.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func newAccountID() (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate account id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

func errAccountsOff() *errs.Error {
	return errs.Errorf(errs.CodeForbidden, "accounts are not enabled on this server")
}

// ---- sign-in ---------------------------------------------------------------

// SignIn returns the account of a provider identity, creating it (with a
// generated handle) on first sign-in. created tells the caller it is new.
func (s *Service) SignIn(provider, providerUserID string) (acct *Account, created bool, err error) {
	if s.store == nil {
		return nil, false, errAccountsOff()
	}
	if provider == "" || providerUserID == "" {
		return nil, false, errs.Errorf(errs.CodeInvalidArgument, "missing sign-in identity")
	}
	if a, ok := s.store.ByIdentity(provider, providerUserID); ok {
		a = publicAccount(a)
		return &a, false, nil
	}
	if s.store.Count() >= s.maxAccounts() {
		return nil, false, errs.Errorf(errs.CodeLimit, "account limit reached")
	}
	id, err := newAccountID()
	if err != nil {
		return nil, false, err
	}
	for attempt := 0; attempt < 12; attempt++ {
		h := SuggestHandle(attempt)
		if ValidateHandle(h) != nil {
			continue
		}
		a := Account{
			ID: id, Provider: provider, ProviderUserID: providerUserID,
			Handle: h, DisplayName: h, ProfilePublic: true, CreatedAt: s.now().UTC(),
		}
		switch err := s.store.Create(a); {
		case err == nil:
			a = publicAccount(a)
			return &a, true, nil
		case errors.Is(err, ErrHandleTaken):
			continue
		case errors.Is(err, ErrIdentityExists):
			// Two sign-ins raced; the other one won.
			if ex, ok := s.store.ByIdentity(provider, providerUserID); ok {
				ex = publicAccount(ex)
				return &ex, false, nil
			}
			return nil, false, err
		default:
			return nil, false, err
		}
	}
	return nil, false, errs.Errorf(errs.CodeLimit, "could not find a free handle; try again")
}

func (s *Service) maxAccounts() int {
	if s.opts.MaxAccounts > 0 {
		return s.opts.MaxAccounts
	}
	return DefaultMaxAccounts
}

func (s *Service) loginTTL() time.Duration {
	if s.opts.LoginTTL > 0 {
		return s.opts.LoginTTL
	}
	return DefaultLoginTTL
}

// StartLogin creates a login for the account and returns the secret token for
// the cookie. Every call makes a new token, so a token that existed before
// signing in is never reused (no session fixation).
func (s *Service) StartLogin(accountID string) (token string, expires time.Time, err error) {
	if s.store == nil {
		return "", time.Time{}, errAccountsOff()
	}
	if _, ok := s.store.Get(accountID); !ok {
		return "", time.Time{}, errs.Errorf(errs.CodeNotFound, "account not found")
	}
	token, hash, err := newToken()
	if err != nil {
		return "", time.Time{}, err
	}
	now := s.now().UTC()
	expires = now.Add(s.loginTTL())
	if err := s.store.PutLogin(Login{TokenHash: hash, AccountID: accountID, CreatedAt: now, ExpiresAt: expires}); err != nil {
		return "", time.Time{}, err
	}
	return token, expires, nil
}

// Authenticate resolves a cookie token to its account. Unknown, expired and
// malformed tokens give ok=false; an expired login is removed.
func (s *Service) Authenticate(token string) (*Account, bool) {
	if s.store == nil || len(token) < 20 || len(token) > 128 {
		return nil, false
	}
	hash := HashToken(token)
	l, ok := s.store.GetLogin(hash)
	if !ok {
		return nil, false
	}
	if !s.now().Before(l.ExpiresAt) {
		if err := s.store.DeleteLogin(hash); err != nil {
			log.Printf("account: delete expired login: %v", err)
		}
		return nil, false
	}
	a, ok := s.store.Get(l.AccountID)
	if !ok {
		return nil, false
	}
	a = publicAccount(a)
	return &a, true
}

// EndLogin signs a token out. Unknown tokens are ignored.
func (s *Service) EndLogin(token string) {
	if s.store == nil || token == "" {
		return
	}
	if err := s.store.DeleteLogin(HashToken(token)); err != nil {
		log.Printf("account: delete login: %v", err)
	}
}

// ---- account management ----------------------------------------------------

// GetAccount returns the account with the given id.
func (s *Service) GetAccount(id string) (*Account, error) {
	if s.store == nil {
		return nil, errAccountsOff()
	}
	a, ok := s.store.Get(id)
	if !ok {
		return nil, errs.Errorf(errs.CodeNotFound, "account not found")
	}
	a = publicAccount(a)
	return &a, nil
}

// Lookup returns an account by id without masking a missing store as an error.
func (s *Service) Lookup(id string) (Account, bool) {
	if s.store == nil || id == "" {
		return Account{}, false
	}
	a, ok := s.store.Get(id)
	if !ok {
		return Account{}, false
	}
	return publicAccount(a), true
}

// LookupHandle returns an account by handle.
func (s *Service) LookupHandle(handle string) (Account, bool) {
	if s.store == nil {
		return Account{}, false
	}
	a, ok := s.store.ByHandle(NormalizeHandle(handle))
	if !ok {
		return Account{}, false
	}
	return publicAccount(a), true
}

// HandleChangeableAt is when the account may change its handle again (zero =
// now).
func (s *Service) HandleChangeableAt(a *Account) time.Time {
	if a == nil || a.HandleChangedAt.IsZero() {
		return time.Time{}
	}
	cd := s.opts.HandleCooldown
	if cd <= 0 {
		cd = DefaultHandleCooldown
	}
	return a.HandleChangedAt.Add(cd)
}

// UpdateAccount applies a player's changes. A new handle is validated, must be
// free (case-insensitively) and respects the cooldown.
func (s *Service) UpdateAccount(id string, u AccountUpdate) (*Account, error) {
	if s.store == nil {
		return nil, errAccountsOff()
	}
	var newName string
	if u.DisplayName != nil {
		n, err := CleanAccountDisplayName(*u.DisplayName, s.opts.MaxDisplayName)
		if err != nil {
			return nil, err
		}
		newName = n
	}
	var newHandle string
	if u.Handle != nil {
		newHandle = NormalizeHandle(*u.Handle)
		if err := ValidateHandle(newHandle); err != nil {
			return nil, err
		}
	}
	now := s.now().UTC()
	a, err := s.store.Update(id, func(a *Account) error {
		if u.Handle != nil && newHandle != a.Handle {
			if next := s.HandleChangeableAt(a); now.Before(next) {
				return errs.Errorf(errs.CodeLimit, "you can change your handle again after %s", next.Format("2 Jan 15:04 UTC"))
			}
			// Keep a display name that simply mirrored the old handle in step.
			if u.DisplayName == nil && a.DisplayName == a.Handle {
				a.DisplayName = newHandle
			}
			a.Handle = newHandle
			a.HandleChangedAt = now
		}
		if u.DisplayName != nil {
			if newName == "" {
				newName = a.Handle
			}
			a.DisplayName = newName
		}
		if u.ProfilePublic != nil {
			a.ProfilePublic = *u.ProfilePublic
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrHandleTaken) {
			return nil, errs.Errorf(errs.CodeInvalidArgument, "that handle is already taken")
		}
		return nil, err
	}
	a = publicAccount(a)
	return &a, nil
}

// DeleteAccount removes the account and its logins. The OnDelete hook runs
// first so each game can detach its own data (for motor: sessions become guest
// runs under a fresh random name).
func (s *Service) DeleteAccount(id string) error {
	if s.store == nil {
		return errAccountsOff()
	}
	if _, ok := s.store.Get(id); !ok {
		return errs.Errorf(errs.CodeNotFound, "account not found")
	}
	if fn := s.deleteHook(); fn != nil {
		if err := fn(id); err != nil {
			return err
		}
	}
	return s.store.Delete(id)
}

// GetProfile returns a player's public identity. A private profile is
// NOT_FOUND for everyone but its owner, so its existence is not revealed.
func (s *Service) GetProfile(handle, viewerAccountID string) (*Profile, error) {
	if s.store == nil {
		return nil, errs.Errorf(errs.CodeNotFound, "profile not found")
	}
	a, ok := s.store.ByHandle(NormalizeHandle(handle))
	if !ok || (!a.ProfilePublic && a.ID != viewerAccountID) {
		return nil, errs.Errorf(errs.CodeNotFound, "profile not found")
	}
	return &Profile{Account: publicAccount(a), IsMine: a.ID == viewerAccountID}, nil
}

// ByID is the internal lookup used by games (leaderboards, session ownership).
func (s *Service) ByID(id string) (Account, bool) { return s.Lookup(id) }

// ByHandle is the internal lookup for handles.
func (s *Service) ByHandle(handle string) (Account, bool) { return s.LookupHandle(handle) }
