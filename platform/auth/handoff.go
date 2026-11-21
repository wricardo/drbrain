package auth

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"
)

// Handoff codes carry a completed OAuth sign-in from the callback redirect to
// the SPA without putting a token in the URL. They live for one minute, are
// single use, and are accepted only from the same origin.
const (
	codeTTL         = time.Minute
	maxPendingCodes = 5000
	// SigninCodeParam is the query parameter the callback adds to `next`; the
	// SPA strips it after exchanging the code.
	SigninCodeParam = "signin_code"
)

// newCode stores a one-time handoff for accountID.
func (a *Auth) newCode(accountID string) (string, error) {
	code, err := randomString(32)
	if err != nil {
		return "", err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.codes) >= maxPendingCodes {
		now := a.now()
		for k, v := range a.codes {
			if !now.Before(v.expires) {
				delete(a.codes, k)
			}
		}
		if len(a.codes) >= maxPendingCodes {
			return "", errors.New("auth: too many sign-ins in progress")
		}
	}
	a.codes[code] = pendingCode{accountID: accountID, expires: a.now().Add(codeTTL)}
	return code, nil
}

// takeCode consumes a handoff code. A code works at most once.
func (a *Auth) takeCode(code string) (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	c, ok := a.codes[code]
	delete(a.codes, code)
	return c.accountID, ok && a.now().Before(c.expires)
}

// tokenResponse is what POST /auth/token returns.
type tokenResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// handleToken exchanges a one-time callback code for an access token.
func (a *Auth) handleToken(w http.ResponseWriter, r *http.Request) {
	if !a.SameOrigin(r) {
		http.Error(w, "cross-site request refused", http.StatusForbidden)
		return
	}
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		http.Error(w, "content type must be application/json", http.StatusUnsupportedMediaType)
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	accountID, ok := a.takeCode(strings.TrimSpace(body.Code))
	if !ok {
		http.Error(w, "invalid or expired code", http.StatusUnauthorized)
		return
	}
	token, expires, err := a.IssueToken(accountID)
	if err != nil {
		log.Printf("auth: issue token: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(tokenResponse{Token: token, ExpiresAt: expires})
}
