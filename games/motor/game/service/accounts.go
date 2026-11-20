package service

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/wricardo/drbrain/platform/account"
	"github.com/wricardo/drbrain/platform/errs"
)

// MaxClaims bounds one claimSessions call.
const MaxClaims = 100

// Claim outcomes.
const (
	ClaimAttached        = "ATTACHED"
	ClaimAlreadyAttached = "ALREADY_YOURS"
	ClaimRejected        = "REJECTED"
)

// SessionClaim is one session a browser wants to attach to its account.
type SessionClaim struct {
	SessionID  string
	OwnerToken string
}

// ClaimResult is the outcome for one claim.
type ClaimResult struct {
	SessionID string
	Status    string
}

// Account is the global account type, kept as an alias for motor code.
type Account = account.Account

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
	var owner *account.Account
	if accountID != "" {
		if svc.opts.Accounts == nil {
			accountID = ""
		} else if a, ok := svc.opts.Accounts.Lookup(accountID); ok {
			owner = &a
		} else {
			accountID = ""
		}
	}
	if owner != nil && strings.TrimSpace(displayName) == "" {
		displayName = owner.DisplayName
	}
	token, hash, err := newOwnerToken()
	if err != nil {
		return nil, "", err
	}
	s, err := svc.createSession(mapID, displayName, accountID, hash)
	if err != nil {
		return nil, "", err
	}
	return s, token, nil
}

func newOwnerToken() (token, hash string, err error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", "", fmt.Errorf("generate token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(b[:])
	return token, account.HashToken(token), nil
}

// ClaimSessions attaches sessions to an account. A session is attached only
// when the owner token matches its stored hash (constant-time compare) and it
// belongs to no account. One already attached to this account reports
// ALREADY_YOURS; every other case is REJECTED, and a session attached to
// another account can never be re-attached.
func (svc *gameService) ClaimSessions(accountID string, claims []SessionClaim) ([]ClaimResult, error) {
	if svc.opts.Accounts == nil {
		return nil, errs.Errorf(errs.CodeForbidden, "accounts are not enabled on this server")
	}
	if _, ok := svc.opts.Accounts.Lookup(accountID); !ok {
		return nil, errs.Errorf(errs.CodeUnauthenticated, "sign in first")
	}
	if len(claims) > MaxClaims {
		return nil, errs.Errorf(errs.CodeInvalidArgument, "too many claims (%d, max %d)", len(claims), MaxClaims)
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
	got := account.HashToken(c.OwnerToken)
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
func (svc *gameService) AuthorizeSessionChange(id string, actor SessionActor) error {
	s, err := svc.get(id)
	if err != nil {
		return err
	}
	owner, hash := sessionProof(s)
	if owner == "" {
		return nil
	}
	if svc.opts.Accounts == nil {
		return nil
	}
	if _, ok := svc.opts.Accounts.Lookup(owner); !ok {
		return nil
	}
	if actor.AccountID == owner {
		return nil
	}
	if tokenMatches(actor.OwnerToken, hash) {
		return nil
	}
	return errs.Errorf(errs.CodeForbidden, "this run belongs to a signed-in player; only they can delete or rename it")
}

// AuthorizeSessionEdit decides whether actor may write a session's program.
func (svc *gameService) AuthorizeSessionEdit(id string, actor SessionActor) error {
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
	return errs.Errorf(errs.CodeForbidden, "this run belongs to whoever created it; only they can change its program (share its owner token to let someone else program it)")
}

// sessionProof reads a session's ownership fields. Caller must not hold the lock.
func sessionProof(s *Session) (owner, hash string) {
	s.Lock()
	defer s.Unlock()
	return s.AccountID, s.OwnerHash
}

// tokenMatches compares an owner token with a stored hash in constant time.
func tokenMatches(token, hash string) bool {
	return token != "" && hash != "" &&
		subtle.ConstantTimeCompare([]byte(account.HashToken(token)), []byte(hash)) == 1
}

// accountFor looks up an account for a board row; false when accounts are off
// or the account no longer exists.
func (svc *gameService) accountFor(id string, cache map[string]*account.Account) *account.Account {
	if id == "" || svc.opts.Accounts == nil {
		return nil
	}
	if a, ok := cache[id]; ok {
		return a
	}
	var out *account.Account
	if a, ok := svc.opts.Accounts.Lookup(id); ok {
		out = &a
	}
	cache[id] = out
	return out
}

// publicAccountName is the name a board shows for an account row.
func publicAccountName(a *account.Account, max int) (name, handle string) {
	if a == nil || !a.ProfilePublic {
		return account.PrivatePlayerName, ""
	}
	return PublicName(a.DisplayName, max), a.Handle
}

// DetachAccount turns every session owned by id into a guest session under a
// fresh random name. It runs from the global account service's delete hook.
func (svc *gameService) DetachAccount(id string) error {
	for _, s := range svc.sessions.List() {
		s.Lock()
		owned := s.AccountID == id
		var snap *Session
		if owned {
			s.AccountID = ""
			s.DisplayName = account.GuestName(svc.opts.MaxDisplayName)
			touch(s)
			snap = s.Clone()
		}
		s.Unlock()
		if owned {
			svc.persist(s)
			svc.broadcast(snap)
		}
	}
	return nil
}
