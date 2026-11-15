package auth

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/wricardo/drbrain-motor-programming/game/service"
)

// Password methods of the shared fake: an in-memory username/password store with
// the same error contract as the real service.
type fakePasswords struct {
	byHandle map[string]string // handle -> password
}

var fakePw = map[*fakeAccounts]*fakePasswords{}

func (f *fakeAccounts) pw() *fakePasswords {
	f.mu.Lock()
	defer f.mu.Unlock()
	if fakePw[f] == nil {
		fakePw[f] = &fakePasswords{byHandle: map[string]string{}}
	}
	return fakePw[f]
}

func (f *fakeAccounts) RegisterPassword(handle, password string) (*service.Account, error) {
	handle = service.NormalizeHandle(handle)
	if err := service.ValidateHandle(handle); err != nil {
		return nil, err
	}
	if err := service.ValidatePassword(password, handle); err != nil {
		return nil, err
	}
	p := f.pw()
	f.mu.Lock()
	if _, taken := p.byHandle[handle]; taken {
		f.mu.Unlock()
		return nil, service.Errorf(service.CodeInvalidArgument, "that username is already taken")
	}
	p.byHandle[handle] = password
	f.mu.Unlock()
	a, _, _ := f.SignIn("password", handle)
	a.Handle = handle
	return a, nil
}

func (f *fakeAccounts) VerifyPassword(handle, password string) (*service.Account, error) {
	handle = service.NormalizeHandle(handle)
	p := f.pw()
	f.mu.Lock()
	want, ok := p.byHandle[handle]
	f.mu.Unlock()
	if !ok || want != password {
		return nil, service.ErrBadCredentials
	}
	a, _, _ := f.SignIn("password", handle)
	a.Handle = handle
	return a, nil
}

func (h *harness) postJSON(path, body string, hdr ...string) (*http.Response, map[string]any) {
	h.t.Helper()
	req, _ := http.NewRequest(http.MethodPost, h.ts.URL+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", h.ts.URL)
	for i := 0; i+1 < len(hdr); i += 2 {
		if hdr[i+1] == "" {
			req.Header.Del(hdr[i])
		} else {
			req.Header.Set(hdr[i], hdr[i+1])
		}
	}
	resp, err := h.client.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return resp, out
}

func pwHarness(t *testing.T, mut func(*Config)) *harness {
	t.Helper()
	return newHarness(t, func(string) []Provider { return nil }, func(c *Config) {
		c.Password = true
		c.Limiter = NewLimiter(1000, time.Minute, nil)
		if mut != nil {
			mut(c)
		}
	})
}

const goodPw = `{"handle":"speedy_fox","password":"tangerine-ladder-42"}`

func TestPasswordRegisterAndLogin(t *testing.T) {
	h := pwHarness(t, nil)

	resp, out := h.postJSON("/auth/password/register", goodPw)
	if resp.StatusCode != http.StatusOK || out["handle"] != "speedy_fox" {
		t.Fatalf("register = %d %v", resp.StatusCode, out)
	}
	c := setCookieHeader(resp, SessionCookie)
	for _, want := range []string{"HttpOnly", "SameSite=Lax", "Path=/"} {
		if !strings.Contains(c, want) {
			t.Errorf("cookie %q lacks %s", c, want)
		}
	}
	first := h.cookie(SessionCookie)
	if first == nil || len(first.Value) < 20 {
		t.Fatal("no session cookie after registering")
	}
	if _, ok := h.accounts.Authenticate(first.Value); !ok {
		t.Fatal("the cookie is not a valid login")
	}

	// Logging in again rotates the cookie and ends the one the browser arrived with.
	resp, out = h.postJSON("/auth/password/login", `{"handle":"SPEEDY_FOX","password":"tangerine-ladder-42"}`)
	if resp.StatusCode != http.StatusOK || out["handle"] != "speedy_fox" {
		t.Fatalf("login = %d %v", resp.StatusCode, out)
	}
	second := h.cookie(SessionCookie)
	if second.Value == first.Value {
		t.Fatal("the session token was reused (fixation)")
	}
	if _, ok := h.accounts.Authenticate(first.Value); ok {
		t.Fatal("the previous login was not ended")
	}
}

func TestPasswordFailuresReadTheSame(t *testing.T) {
	h := pwHarness(t, nil)
	h.postJSON("/auth/password/register", goodPw)
	h.client.Jar.(*simpleJar).cookies = map[string]*http.Cookie{}

	r1, wrong := h.postJSON("/auth/password/login", `{"handle":"speedy_fox","password":"tangerine-ladder-43"}`)
	r2, unknown := h.postJSON("/auth/password/login", `{"handle":"nobody_here","password":"tangerine-ladder-42"}`)
	if r1.StatusCode != http.StatusUnauthorized || r2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("statuses %d / %d", r1.StatusCode, r2.StatusCode)
	}
	if wrong["error"] != unknown["error"] || wrong["code"] != unknown["code"] {
		t.Fatalf("a wrong password and an unknown user are distinguishable: %v vs %v", wrong, unknown)
	}
	if h.cookie(SessionCookie) != nil {
		t.Fatal("a failed login set a cookie")
	}
}

func TestPasswordRegistrationValidation(t *testing.T) {
	h := pwHarness(t, nil)
	tests := []struct {
		name, body string
		status     int
	}{
		{"weak password", `{"handle":"fresh_one","password":"password123"}`, http.StatusBadRequest},
		{"short password", `{"handle":"fresh_one","password":"short"}`, http.StatusBadRequest},
		{"bad handle", `{"handle":"no spaces","password":"tangerine-ladder-42"}`, http.StatusBadRequest},
		{"blocked handle", `{"handle":"fuck_you","password":"tangerine-ladder-42"}`, http.StatusBadRequest},
		{"unknown field", `{"handle":"fresh_one","password":"tangerine-ladder-42","admin":true}`, http.StatusBadRequest},
		{"not json", `handle=fresh_one`, http.StatusBadRequest},
		{"empty body", ``, http.StatusBadRequest},
		{"oversized body", `{"handle":"fresh_one","password":"` + strings.Repeat("a", 5000) + `"}`, http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, out := h.postJSON("/auth/password/register", tc.body)
			if resp.StatusCode != tc.status {
				t.Fatalf("status = %d %v, want %d", resp.StatusCode, out, tc.status)
			}
			if h.cookie(SessionCookie) != nil {
				t.Fatal("a refused registration set a cookie")
			}
		})
	}
	h.postJSON("/auth/password/register", goodPw)
	h.client.Jar.(*simpleJar).cookies = map[string]*http.Cookie{}
	if resp, _ := h.postJSON("/auth/password/register", goodPw); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("a taken username = %d", resp.StatusCode)
	}
}

// Another site must be able neither to create accounts nor to guess passwords
// through a victim's browser.
func TestPasswordRoutesRefuseCrossSiteAndNonJSON(t *testing.T) {
	h := pwHarness(t, nil)
	h.postJSON("/auth/password/register", goodPw)
	h.client.Jar.(*simpleJar).cookies = map[string]*http.Cookie{}

	tests := []struct {
		name   string
		hdr    []string
		status int
	}{
		{"foreign origin", []string{"Origin", "https://evil.example"}, http.StatusForbidden},
		{"origin null", []string{"Origin", "null"}, http.StatusForbidden},
		{"cross-site metadata, no origin", []string{"Origin", "", "Sec-Fetch-Site", "cross-site"}, http.StatusForbidden},
		{"form encoding", []string{"Content-Type", "application/x-www-form-urlencoded"}, http.StatusUnsupportedMediaType},
		{"text/plain", []string{"Content-Type", "text/plain"}, http.StatusUnsupportedMediaType},
	}
	for _, path := range []string{"/auth/password/login", "/auth/password/register"} {
		for _, tc := range tests {
			t.Run(path+" "+tc.name, func(t *testing.T) {
				resp, _ := h.postJSON(path, goodPw, tc.hdr...)
				if resp.StatusCode != tc.status {
					t.Fatalf("status = %d, want %d", resp.StatusCode, tc.status)
				}
				if h.cookie(SessionCookie) != nil {
					t.Fatal("a refused request set a cookie")
				}
			})
		}
	}
	if len(h.accounts.pw().byHandle) != 1 {
		t.Fatal("a cross-site request created an account")
	}
	// GET is not a way in.
	if resp := h.get("/auth/password/login"); resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET /auth/password/login = %d", resp.StatusCode)
	}
}

func TestPasswordLockout(t *testing.T) {
	h := pwHarness(t, nil)
	h.postJSON("/auth/password/register", goodPw)
	h.client.Jar.(*simpleJar).cookies = map[string]*http.Cookie{}
	bad := `{"handle":"speedy_fox","password":"guess-number-%d"}`

	for i := 0; i < 5; i++ {
		resp, _ := h.postJSON("/auth/password/login", strings.Replace(bad, "%d", string(rune('a'+i)), 1))
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d = %d", i, resp.StatusCode)
		}
	}
	// Locked: even the right password is refused, without being checked.
	resp, out := h.postJSON("/auth/password/login", goodPw)
	if resp.StatusCode != http.StatusTooManyRequests || resp.Header.Get("Retry-After") == "" || out["code"] != service.CodeLimit {
		t.Fatalf("locked login = %d %v (Retry-After %q)", resp.StatusCode, out, resp.Header.Get("Retry-After"))
	}
	if h.cookie(SessionCookie) != nil {
		t.Fatal("a locked login set a cookie")
	}
	// A different client address is not locked by the per-address counter...
	resp, _ = h.postJSON("/auth/password/login", goodPw, "X-Forwarded-For", "203.0.113.50")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("other address = %d", resp.StatusCode)
	}
	// ...and the lock lifts after 15 minutes.
	h.client.Jar.(*simpleJar).cookies = map[string]*http.Cookie{}
	h.advance(16 * time.Minute)
	if resp, _ = h.postJSON("/auth/password/login", goodPw); resp.StatusCode != http.StatusOK {
		t.Fatalf("after the lock = %d", resp.StatusCode)
	}
}

// Failures for one username from many addresses still lock that username.
func TestPasswordLockoutPerUsername(t *testing.T) {
	h := pwHarness(t, nil)
	h.postJSON("/auth/password/register", goodPw)
	h.client.Jar.(*simpleJar).cookies = map[string]*http.Cookie{}
	for i := 0; i < 40; i++ {
		ip := fmt.Sprintf("198.51.100.%d", i+1)
		h.postJSON("/auth/password/login", `{"handle":"speedy_fox","password":"wrong-guess-here"}`, "X-Forwarded-For", ip)
	}
	resp, _ := h.postJSON("/auth/password/login", goodPw, "X-Forwarded-For", "203.0.113.77")
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("after 40 distributed failures = %d", resp.StatusCode)
	}
}

func TestPasswordRoutesOffUnlessEnabled(t *testing.T) {
	g := newFakeGoogle(t)
	h := newHarness(t, func(string) []Provider { return []Provider{g.provider()} }, nil)
	for _, p := range []string{"/auth/password/register", "/auth/password/login"} {
		if resp, _ := h.postJSON(p, goodPw); resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("POST %s with AUTH_PASSWORD off = %d", p, resp.StatusCode)
		}
	}
	for _, p := range h.a.Providers() {
		if p.ID == PasswordProviderID {
			t.Fatal("password is offered while disabled")
		}
	}
	// A "password" OAuth provider path never exists either.
	if resp := h.get("/auth/password/login"); resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET /auth/password/login = %d", resp.StatusCode)
	}
}

func TestPasswordIsListedAsAProviderAndAloneIsEnough(t *testing.T) {
	h := pwHarness(t, nil)
	ps := h.a.Providers()
	if len(ps) != 1 || ps[0].ID != PasswordProviderID || ps[0].Name == "" {
		t.Fatalf("providers = %+v", ps)
	}
	if _, err := New(Config{Accounts: newFakeAccounts(), PublicURL: "http://localhost"}); err == nil {
		t.Fatal("New with no providers and no password succeeded")
	}
}

func TestProvidersFromEnvPassword(t *testing.T) {
	env := func(kv ...string) func(string) string {
		m := map[string]string{}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return func(k string) string { return m[k] }
	}
	if PasswordFromEnv(env()) || PasswordFromEnv(env("AUTH_PASSWORD", "false")) || PasswordFromEnv(env("AUTH_PASSWORD", "")) {
		t.Fatal("password sign-in is on by default")
	}
	for _, v := range []string{"true", "1", "yes", "TRUE"} {
		if !PasswordFromEnv(env("AUTH_PASSWORD", v)) {
			t.Errorf("AUTH_PASSWORD=%s not honoured", v)
		}
	}
	// It needs the public URL like every other way of signing in.
	if _, err := ProvidersFromEnv(env("AUTH_PASSWORD", "true"), ""); err == nil || !strings.Contains(err.Error(), "-public-url") {
		t.Fatalf("err = %v", err)
	}
	if _, err := ProvidersFromEnv(env("AUTH_PASSWORD", "true"), "https://x.example"); err != nil {
		t.Fatal(err)
	}
}

func TestFailTracker(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	f := NewFailTracker(3, time.Minute, 10*time.Minute, func() time.Time { return now })
	f.Fail("a")
	f.Fail("a")
	if b, _ := f.Blocked("a"); b {
		t.Fatal("blocked after 2 of 3")
	}
	f.Fail("a")
	if b, wait := f.Blocked("a"); !b || wait != 10*time.Minute {
		t.Fatalf("Blocked = %v, %v", b, wait)
	}
	if b, _ := f.Blocked("b"); b {
		t.Fatal("keys share a counter")
	}
	// Failures spread beyond the window do not add up.
	f.Fail("c")
	now = now.Add(2 * time.Minute)
	f.Fail("c")
	f.Fail("c")
	if b, _ := f.Blocked("c"); b {
		t.Fatal("old failures counted")
	}
	// The lock expires, and Clear forgets failures.
	now = now.Add(11 * time.Minute)
	if b, _ := f.Blocked("a"); b {
		t.Fatal("lock did not expire")
	}
	f.Fail("d")
	f.Fail("d")
	f.Clear("d")
	f.Fail("d")
	if b, _ := f.Blocked("d"); b {
		t.Fatal("Clear did not reset")
	}
}
