package service

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"

	"github.com/wricardo/drbrain/platform/account"
)

// Actor is who asks to change a session: the signed-in account of the request
// ("" = none) and/or the secret owner token its creator was given.
type Actor struct {
	AccountID  string
	OwnerToken string
}

// NewOwnerToken returns a fresh secret and the hash to store. The secret is 32
// random bytes (256 bits) in unpadded base64url; only its SHA-256 is persisted,
// so a leaked session file cannot be replayed as a credential.
func NewOwnerToken() (token, hash string, err error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", "", fmt.Errorf("generate owner token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(b[:])
	return token, account.HashToken(token), nil
}

// tokenMatches compares an owner token with a stored hash in constant time.
func tokenMatches(token, hash string) bool {
	return token != "" && hash != "" &&
		subtle.ConstantTimeCompare([]byte(account.HashToken(token)), []byte(hash)) == 1
}

// CanPlay reports whether actor may change the session (every state change and
// delete). A session without a stored hash was created before owner tokens and
// stays open. A session whose owning account no longer resolves is NOT open: it
// stays token-gated, so a deleted account cannot turn it into a shared one.
func (s *Session) CanPlay(a Actor) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.canPlayLocked(a)
}

func (s *Session) canPlayLocked(a Actor) bool {
	if s.OwnerTokenHash == "" {
		return true // legacy session: there is no credential to check
	}
	if s.OwnerAccountID != "" && a.AccountID == s.OwnerAccountID {
		return true
	}
	return tokenMatches(a.OwnerToken, s.OwnerTokenHash)
}
