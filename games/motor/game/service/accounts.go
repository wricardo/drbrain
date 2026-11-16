package service

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
)

// Account limits and defaults.
const (
	// DefaultLoginTTL is how long a login lasts. It is fixed, not sliding: the
	// player signs in again after 30 days.
	DefaultLoginTTL = 30 * 24 * time.Hour
	// DefaultHandleCooldown is the minimum time between two handle changes. The
	// first change after sign-up (the handle was generated) is free.
	DefaultHandleCooldown = 24 * time.Hour
	// DefaultMaxAccounts caps how many accounts exist (MAX_ACCOUNTS).
	DefaultMaxAccounts = 10000
	// MaxLoginsPerAccount is how many browsers may be signed in at once; the
	// oldest login is dropped beyond it.
	MaxLoginsPerAccount = 10
	// MaxClaims bounds one claimSessions call.
	MaxClaims = 100
	// PrivatePlayerName is shown on boards for an account whose profile is private.
	PrivatePlayerName = "Private player"

	tokenBytes = 32
)

// CodeUnauthenticated is the extensions.code for operations that need a signed-in player.
const CodeUnauthenticated = "UNAUTHENTICATED"

// Sentinel errors a store returns so the service can retry or report them.
var (
	ErrHandleTaken    = Errorf(CodeInvalidArgument, "that handle is already taken")
	ErrIdentityExists = Errorf(CodeInvalidArgument, "that sign-in identity already has an account")
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

// AccountStore persists accounts and logins (implemented by game/account).
// Create enforces unique handles (case-insensitive) and unique provider
// identities atomically; Update does the same when the handle changes.
type AccountStore interface {
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

// Profile is a player's public page: their account and best run per map.
type Profile struct {
	Account Account
	Runs    []PlayerRun
	// IsMine is true when the viewer is this account.
	IsMine bool
}

// SessionClaim is one session a browser wants to attach to its account.
type SessionClaim struct {
	SessionID  string
	OwnerToken string
}

// Claim outcomes.
const (
	ClaimAttached        = "ATTACHED"
	ClaimAlreadyAttached = "ALREADY_YOURS"
	ClaimRejected        = "REJECTED"
)

// ClaimResult is the outcome for one claim. Rejected covers every reason
// (unknown session, no owner token, wrong token, owned by another account) so
// the answer cannot be used to probe for session ids.
type ClaimResult struct {
	SessionID string
	Status    string
}

// ---- helpers ---------------------------------------------------------------

func (svc *gameService) now() time.Time {
	if svc.opts.Now != nil {
		return svc.opts.Now()
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

func errAccountsOff() *Error {
	return Errorf(CodeForbidden, "accounts are not enabled on this server")
}

// AccountsEnabled reports whether sign-in is available.
func (svc *gameService) AccountsEnabled() bool { return svc.opts.Accounts != nil }

// ---- sign-in ---------------------------------------------------------------

// SignIn returns the account of a provider identity, creating it (with a
// generated handle) on first sign-in. created tells the caller it is new.
func (svc *gameService) SignIn(provider, providerUserID string) (acct *Account, created bool, err error) {
	st := svc.opts.Accounts
	if st == nil {
		return nil, false, errAccountsOff()
	}
	if provider == "" || providerUserID == "" {
		return nil, false, Errorf(CodeInvalidArgument, "missing sign-in identity")
	}
	if a, ok := st.ByIdentity(provider, providerUserID); ok {
		a = publicAccount(a)
		return &a, false, nil
	}
	if st.Count() >= svc.maxAccounts() {
		return nil, false, Errorf(CodeLimit, "account limit reached")
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
			Handle: h, DisplayName: h, ProfilePublic: true, CreatedAt: svc.now().UTC(),
		}
		switch err := st.Create(a); {
		case err == nil:
			a = publicAccount(a)
			return &a, true, nil
		case errors.Is(err, ErrHandleTaken):
			continue
		case errors.Is(err, ErrIdentityExists):
			// Two sign-ins raced; the other one won.
			if ex, ok := st.ByIdentity(provider, providerUserID); ok {
				ex = publicAccount(ex)
				return &ex, false, nil
			}
			return nil, false, err
		default:
			return nil, false, err
		}
	}
	return nil, false, Errorf(CodeLimit, "could not find a free handle; try again")
}

func (svc *gameService) maxAccounts() int {
	if svc.opts.MaxAccounts > 0 {
		return svc.opts.MaxAccounts
	}
	return DefaultMaxAccounts
}

func (svc *gameService) loginTTL() time.Duration {
	if svc.opts.LoginTTL > 0 {
		return svc.opts.LoginTTL
	}
	return DefaultLoginTTL
}

// StartLogin creates a login for the account and returns the secret token for
// the cookie. Every call makes a new token, so a token that existed before
// signing in is never reused (no session fixation).
func (svc *gameService) StartLogin(accountID string) (token string, expires time.Time, err error) {
	st := svc.opts.Accounts
	if st == nil {
		return "", time.Time{}, errAccountsOff()
	}
	if _, ok := st.Get(accountID); !ok {
		return "", time.Time{}, Errorf(CodeNotFound, "account not found")
	}
	token, hash, err := newToken()
	if err != nil {
		return "", time.Time{}, err
	}
	now := svc.now().UTC()
	expires = now.Add(svc.loginTTL())
	if err := st.PutLogin(Login{TokenHash: hash, AccountID: accountID, CreatedAt: now, ExpiresAt: expires}); err != nil {
		return "", time.Time{}, err
	}
	return token, expires, nil
}

// Authenticate resolves a cookie token to its account. Unknown, expired and
// malformed tokens give ok=false; an expired login is removed.
func (svc *gameService) Authenticate(token string) (*Account, bool) {
	st := svc.opts.Accounts
	if st == nil || len(token) < 20 || len(token) > 128 {
		return nil, false
	}
	hash := HashToken(token)
	l, ok := st.GetLogin(hash)
	if !ok {
		return nil, false
	}
	if !svc.now().Before(l.ExpiresAt) {
		if err := st.DeleteLogin(hash); err != nil {
			log.Printf("service: delete expired login: %v", err)
		}
		return nil, false
	}
	a, ok := st.Get(l.AccountID)
	if !ok {
		return nil, false
	}
	a = publicAccount(a)
	return &a, true
}

// EndLogin signs a token out. Unknown tokens are ignored.
func (svc *gameService) EndLogin(token string) {
	st := svc.opts.Accounts
	if st == nil || token == "" {
		return
	}
	if err := st.DeleteLogin(HashToken(token)); err != nil {
		log.Printf("service: delete login: %v", err)
	}
}

// ---- account management ----------------------------------------------------

// GetAccount returns the account with the given id.
func (svc *gameService) GetAccount(id string) (*Account, error) {
	st := svc.opts.Accounts
	if st == nil {
		return nil, errAccountsOff()
	}
	a, ok := st.Get(id)
	if !ok {
		return nil, Errorf(CodeNotFound, "account not found")
	}
	a = publicAccount(a)
	return &a, nil
}

// HandleChangeableAt is when the account may change its handle again (zero =
// now).
func (svc *gameService) HandleChangeableAt(a *Account) time.Time {
	if a == nil || a.HandleChangedAt.IsZero() {
		return time.Time{}
	}
	cd := svc.opts.HandleCooldown
	if cd <= 0 {
		cd = DefaultHandleCooldown
	}
	return a.HandleChangedAt.Add(cd)
}

// UpdateAccount applies a player's changes. A new handle is validated, must be
// free (case-insensitively) and respects the cooldown.
func (svc *gameService) UpdateAccount(id string, u AccountUpdate) (*Account, error) {
	st := svc.opts.Accounts
	if st == nil {
		return nil, errAccountsOff()
	}
	var newName string
	if u.DisplayName != nil {
		n, err := CleanAccountDisplayName(*u.DisplayName, svc.opts.MaxDisplayName)
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
	now := svc.now().UTC()
	a, err := st.Update(id, func(a *Account) error {
		if u.Handle != nil && newHandle != a.Handle {
			if next := svc.HandleChangeableAt(a); now.Before(next) {
				return Errorf(CodeLimit, "you can change your handle again after %s", next.Format("2 Jan 15:04 UTC"))
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
			return nil, Errorf(CodeInvalidArgument, "that handle is already taken")
		}
		return nil, err
	}
	a = publicAccount(a)
	return &a, nil
}

// DeleteAccount removes the account and everything tied to it. Decision: the
// runs it owned stay on the leaderboards as ordinary guest entries under a
// fresh random guest name. Their link to the account (and so to the handle and
// display name it chose) is cut, and the profile page and logins disappear.
// Sessions are detached first: if the process stops half-way the account can
// simply be deleted again, and a session pointing at a missing account is
// treated as a guest one.
func (svc *gameService) DeleteAccount(id string) error {
	st := svc.opts.Accounts
	if st == nil {
		return errAccountsOff()
	}
	if _, ok := st.Get(id); !ok {
		return Errorf(CodeNotFound, "account not found")
	}
	for _, s := range svc.sessions.List() {
		s.Lock()
		owned := s.AccountID == id
		var snap *Session
		if owned {
			s.AccountID = ""
			s.DisplayName = GuestName(svc.opts.MaxDisplayName)
			touch(s)
			snap = s.Clone()
		}
		s.Unlock()
		if owned {
			svc.persist(s)
			svc.broadcast(snap)
		}
	}
	return st.Delete(id)
}

// GetProfile returns a player's profile. A private profile is NOT_FOUND for
// everyone but its owner, so its existence is not revealed.
func (svc *gameService) GetProfile(handle, viewerAccountID string) (*Profile, error) {
	st := svc.opts.Accounts
	if st == nil {
		return nil, Errorf(CodeNotFound, "profile not found")
	}
	a, ok := st.ByHandle(NormalizeHandle(handle))
	if !ok || (!a.ProfilePublic && a.ID != viewerAccountID) {
		return nil, Errorf(CodeNotFound, "profile not found")
	}
	runs := svc.playerRuns(func(r boardRow) bool { return r.accountID == a.ID && r.acct != nil }, viewerAccountID)
	return &Profile{Account: publicAccount(a), Runs: runs, IsMine: a.ID == viewerAccountID}, nil
}

// ---- owned sessions --------------------------------------------------------

// CreateOwnedSession is CreateSession for a caller that may be signed in: the
// session is attached to accountID (when not empty) and a secret owner token is
// returned. Only the token's hash is stored. The token is what lets a browser
// attach the session to an account later (ClaimSessions); a session id alone
// never can, because ids are public (spectator links).
//
// An empty displayName from a signed-in caller defaults to the account's display
// name, not a random guest name: the player is not a guest, and the name is what
// the play and watch pages show for the session.
func (svc *gameService) CreateOwnedSession(mapID, displayName, accountID string) (*Session, string, error) {
	var owner *Account
	if accountID != "" {
		if st := svc.opts.Accounts; st == nil {
			accountID = ""
		} else if a, ok := st.Get(accountID); ok {
			owner = &a
		} else {
			accountID = ""
		}
	}
	if owner != nil && strings.TrimSpace(displayName) == "" {
		displayName = owner.DisplayName
	}
	token, hash, err := newToken()
	if err != nil {
		return nil, "", err
	}
	s, err := svc.createSession(mapID, displayName, accountID, hash)
	if err != nil {
		return nil, "", err
	}
	return s, token, nil
}

// ClaimSessions attaches sessions to an account. A session is attached only
// when the owner token matches its stored hash (constant-time compare) and it
// belongs to no account. One already attached to this account reports
// ALREADY_YOURS; every other case is REJECTED, and a session attached to
// another account can never be re-attached.
func (svc *gameService) ClaimSessions(accountID string, claims []SessionClaim) ([]ClaimResult, error) {
	st := svc.opts.Accounts
	if st == nil {
		return nil, errAccountsOff()
	}
	if _, ok := st.Get(accountID); !ok {
		return nil, Errorf(CodeUnauthenticated, "sign in first")
	}
	if len(claims) > MaxClaims {
		return nil, Errorf(CodeInvalidArgument, "too many claims (%d, max %d)", len(claims), MaxClaims)
	}
	out := make([]ClaimResult, 0, len(claims))
	for _, c := range claims {
		out = append(out, ClaimResult{SessionID: c.SessionID, Status: svc.claimOne(accountID, c)})
	}
	return out, nil
}

func (svc *gameService) claimOne(accountID string, c SessionClaim) string {
	s, ok := svc.sessions.Get(c.SessionID)
	if !ok || c.OwnerToken == "" {
		return ClaimRejected
	}
	got := HashToken(c.OwnerToken)
	s.Lock()
	// Compare fixed-length digests in constant time. A session without a
	// stored hash (created before owner tokens) can never match.
	if s.OwnerHash == "" || subtle.ConstantTimeCompare([]byte(got), []byte(s.OwnerHash)) != 1 {
		s.Unlock()
		return ClaimRejected
	}
	switch {
	case s.AccountID == accountID:
		s.Unlock()
		return ClaimAlreadyAttached
	case s.AccountID != "":
		s.Unlock()
		return ClaimRejected
	}
	s.AccountID = accountID
	touch(s)
	snap := s.Clone()
	s.Unlock()
	svc.persist(s)
	svc.broadcast(snap)
	return ClaimAttached
}

// SessionActor is who asks to change or play a session: the signed-in account of
// the request (""= none) and/or the owner token the session's creator was given.
type SessionActor struct {
	AccountID  string
	OwnerToken string
}

// AuthorizeSessionChange decides whether actor may delete or rename a session.
// Guest sessions stay open to anyone who knows the id, as they always were (a
// human and an agent can share one). A session that belongs to a signed-in
// player's account is theirs: it needs that account's login, or the secret
// owner token (constant-time compare of hashes). Playing is gated separately, by
// AuthorizeSessionPlay, because a shared watch link should be read-only. An
// unknown session is NOT_FOUND, as before. A link to an account that no longer
// exists (or accounts being off) counts as a guest session.
func (svc *gameService) AuthorizeSessionChange(id string, actor SessionActor) error {
	s, err := svc.get(id)
	if err != nil {
		return err
	}
	owner, hash := sessionProof(s)
	if owner == "" {
		return nil
	}
	st := svc.opts.Accounts
	if st == nil {
		return nil
	}
	if _, ok := st.Get(owner); !ok {
		return nil
	}
	if actor.AccountID == owner {
		return nil
	}
	if tokenMatches(actor.OwnerToken, hash) {
		return nil
	}
	return Errorf(CodeForbidden, "this run belongs to a signed-in player; only they can delete or rename it")
}

// AuthorizeSessionPlay decides whether actor may play a session: setProgram,
// run, step, pause and reset. Unlike AuthorizeSessionChange the rule is the
// same for guests and signed-in players, because the owner token is minted for
// every session: the creator proves it with that token (the browser keeps it
// in its own storage, so a guest needs no account) or, for a session created
// while signed in, with the owning account's login. Whoever holds either can
// play, which is how a session is shared with a second person or an AI agent.
//
// A session without an owner hash predates owner tokens (or was made through
// the token-less CreateSession): there is nothing to compare against, so it
// stays open rather than becoming unplayable for everyone.
func (svc *gameService) AuthorizeSessionPlay(id string, actor SessionActor) error {
	s, err := svc.get(id)
	if err != nil {
		return err
	}
	owner, hash := sessionProof(s)
	if hash == "" {
		return nil
	}
	if owner != "" && actor.AccountID == owner {
		return nil
	}
	if tokenMatches(actor.OwnerToken, hash) {
		return nil
	}
	return Errorf(CodeForbidden, "this run belongs to whoever created it; only they can play it (share its owner token to let someone else play)")
}

// sessionProof reads a session's ownership fields. Caller must not hold the lock.
func sessionProof(s *Session) (owner, hash string) {
	s.Lock()
	defer s.Unlock()
	return s.AccountID, s.OwnerHash
}

// tokenMatches compares an owner token with a stored hash in constant time. An
// empty token or hash never matches (a session without a hash cannot be proven).
func tokenMatches(token, hash string) bool {
	return token != "" && hash != "" &&
		subtle.ConstantTimeCompare([]byte(HashToken(token)), []byte(hash)) == 1
}

// accountFor looks up an account for a board row; false when accounts are off
// or the account no longer exists.
func (svc *gameService) accountFor(id string, cache map[string]*Account) *Account {
	if id == "" || svc.opts.Accounts == nil {
		return nil
	}
	if a, ok := cache[id]; ok {
		return a
	}
	var out *Account
	if a, ok := svc.opts.Accounts.Get(id); ok {
		out = &a
	}
	cache[id] = out
	return out
}

// publicAccountName is the name a board shows for an account row.
func publicAccountName(a *Account, max int) (name, handle string) {
	if a == nil || !a.ProfilePublic {
		return PrivatePlayerName, ""
	}
	return PublicName(a.DisplayName, max), a.Handle
}
