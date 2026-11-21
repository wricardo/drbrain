package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wricardo/drbrain/platform/account"
)

// DefaultTokenTTL is how long an access token lasts. It is fixed, not sliding:
// a client must sign in again when it expires. There is no refresh token, so
// logout is client-side and a leaked token stays valid until then; its blast
// radius is bounded by the TTL and by the account lookup done on every request
// (a deleted account stops working immediately).
const DefaultTokenTTL = 24 * time.Hour

// Token issuer and audience. Both are checked, so a token minted for another
// service signed with the same key is not accepted here.
const (
	tokenIssuer   = "drbrain"
	tokenAudience = "drbrain-graphql"
)

// ErrInvalidToken is returned by ParseToken for any malformed, mis-signed or
// expired token. Callers must not distinguish the causes to clients.
var ErrInvalidToken = errors.New("invalid token")

// tokenClaims is the JWT payload. Only these fields are used; anything else an
// issuer might add is ignored.
type tokenClaims struct {
	Subject   string `json:"sub"`
	Issuer    string `json:"iss"`
	Audience  string `json:"aud"`
	IssuedAt  int64  `json:"iat"`
	ExpiresAt int64  `json:"exp"`
}

// IssueToken mints a signed access token for accountID. The token is a compact
// HS256 JWS (header.payload.signature) carrying sub/iss/aud/iat/exp.
func (a *Auth) IssueToken(accountID string) (string, time.Time, error) {
	if accountID == "" {
		return "", time.Time{}, errors.New("auth: empty account id")
	}
	now := a.now().UTC()
	expires := now.Add(a.tokenTTL)
	payload, err := json.Marshal(tokenClaims{
		Subject: accountID, Issuer: tokenIssuer, Audience: tokenAudience,
		IssuedAt: now.Unix(), ExpiresAt: expires.Unix(),
	})
	if err != nil {
		return "", time.Time{}, err
	}
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	body := base64.RawURLEncoding.EncodeToString(payload)
	signingInput := header + "." + body
	sig := hmac.New(sha256.New, a.jwtKey)
	sig.Write([]byte(signingInput))
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig.Sum(nil)), expires, nil
}

// ParseToken validates raw and returns the account it names. Unknown accounts
// (deleted since the token was minted) are rejected, and expired or expired-
// soon tokens are rejected before any lookup.
func (a *Auth) ParseToken(raw string) (*account.Account, error) {
	claims, err := a.verify(raw)
	if err != nil {
		return nil, err
	}
	acct, ok := a.cfg.Accounts.Lookup(claims.Subject)
	if !ok {
		return nil, ErrInvalidToken
	}
	return &acct, nil
}

func (a *Auth) verify(raw string) (tokenClaims, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return tokenClaims{}, ErrInvalidToken
	}
	mac := hmac.New(sha256.New, a.jwtKey)
	mac.Write([]byte(parts[0] + "." + parts[1]))
	want := mac.Sum(nil)
	got, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || subtle.ConstantTimeCompare(got, want) != 1 {
		return tokenClaims{}, ErrInvalidToken
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return tokenClaims{}, ErrInvalidToken
	}
	var claims tokenClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return tokenClaims{}, ErrInvalidToken
	}
	now := a.now().Unix()
	if claims.Subject == "" || claims.Issuer != tokenIssuer || claims.Audience != tokenAudience ||
		claims.IssuedAt > now+60 || claims.ExpiresAt <= now {
		return tokenClaims{}, ErrInvalidToken
	}
	return claims, nil
}

// BearerToken extracts the token from an Authorization header value
// ("Bearer <token>", case-insensitive scheme). Empty means no credentials.
func BearerToken(header string) string {
	scheme, token, ok := strings.Cut(strings.TrimSpace(header), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return strings.TrimSpace(token)
}

// newJWTKey returns the configured signing key, or a random per-process key
// when none is configured (development convenience: tokens do not survive a
// restart and do not work across instances; production must set the secret).
func newJWTKey(configured string) []byte {
	if k := []byte(strings.TrimSpace(configured)); len(k) > 0 {
		return k
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("auth: generate JWT key: %v", err))
	}
	return b
}
