package session

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"

	"github.com/wricardo/drbrain/platform/account"
)

// Actor is who asks to change a session: the signed-in account of the request
// ("" = none) and/or the owner token the session's creator was given.
type Actor struct {
	AccountID  string
	OwnerToken string
}

// Claim outcomes. They match the ClaimStatus enum in the GraphQL schema.
const (
	ClaimAttached        = "ATTACHED"
	ClaimAlreadyAttached = "ALREADY_YOURS"
	ClaimRejected        = "REJECTED"
)

// MaxClaims bounds one claimSessions call.
const MaxClaims = 100

// newOwnerToken returns a fresh secret and the hash to store. The secret is 32
// random bytes (256 bits) in unpadded base64url; only its SHA-256 is persisted,
// so a leaked save file cannot be replayed as a credential.
func newOwnerToken() (token, hash string, err error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", "", fmt.Errorf("generate token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(b[:])
	return token, account.HashToken(token), nil
}

// tokenMatches compares an owner token with a stored hash in constant time.
func tokenMatches(token, hash string) bool {
	return token != "" && hash != "" &&
		subtle.ConstantTimeCompare([]byte(account.HashToken(token)), []byte(hash)) == 1
}

// CanPlay reports whether actor may change the session's state (every gated
// mutation, and delete). A session without a stored hash was created before
// owner tokens and stays open. A session whose linked account no longer
// resolves is NOT open: it stays token-gated, so a deleted account cannot turn
// a save into a shared one.
func (s *Session) CanPlay(a Actor) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.canPlayLocked(a)
}

func (s *Session) canPlayLocked(a Actor) bool {
	if s.ownerHash == "" {
		return true // legacy save: there is no credential to check
	}
	if s.accountID != "" && a.AccountID == s.accountID {
		return true
	}
	return tokenMatches(a.OwnerToken, s.ownerHash)
}

// OwnerAccountID returns the account the session belongs to ("" = guest).
func (s *Session) OwnerAccountID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.accountID
}

// claim attaches the session to accountID when token proves ownership. It never
// re-attaches a session owned by another account. Caller must hold no lock.
func (s *Session) claim(accountID, token string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ownerHash == "" || !tokenMatches(token, s.ownerHash) {
		return ClaimRejected
	}
	switch {
	case s.accountID == accountID:
		return ClaimAlreadyAttached
	case s.accountID != "":
		return ClaimRejected
	}
	s.accountID = accountID
	return ClaimAttached
}

// sanitizeAccountID drops an account link that is not a plausible account id (a
// hand-edited file must not smuggle odd strings into later lookups).
func sanitizeAccountID(id string) string {
	if len(id) != 24 {
		return ""
	}
	for _, r := range id {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return ""
		}
	}
	return id
}

// sanitizeHash keeps only a 64-character lower-case hex SHA-256.
func sanitizeHash(h string) string {
	if len(h) != 64 {
		return ""
	}
	for _, r := range h {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return ""
		}
	}
	return h
}

// newSessionID is 8 random bytes as 16 lower-case hex characters. The id is not
// a credential, but a random one cannot be guessed or enumerated by signup time.
func newSessionID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate session id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
