package auth

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
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

const (
	goodHandle = "speedy_fox"
	goodPw     = "tangerine-ladder-42"
)

// browserReq builds the POST a browser's GraphQL call carries: the Origin is
// this site's, which is what the same-origin check looks at.
func (h *harness) browserReq(hdr ...string) *http.Request {
	h.t.Helper()
	req := httptest.NewRequest(http.MethodPost, h.ts.URL+"/graphql", nil)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", h.ts.URL)
	// A loopback peer, as a real browser behind the local reverse proxy is: that
	// is what makes ClientIP trust the X-Forwarded-For the tests set.
	req.RemoteAddr = "127.0.0.1:1234"
	if c := h.cookie(SessionCookie); c != nil {
		req.AddCookie(c)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		if hdr[i+1] == "" {
			req.Header.Del(hdr[i])
		} else {
			req.Header.Set(hdr[i], hdr[i+1])
		}
	}
	return req
}

// session is what PasswordSignIn hands to the transport: the cookie it must set.
type session struct {
	token   string
	expires time.Time
	set     bool
}

// signIn calls PasswordSignIn as the browser would and returns the account, the
// session it asked to set and the error.
func (h *harness) signIn(register bool, handle, password string, hdr ...string) (*service.Account, session, error) {
	h.t.Helper()
	var s session
	acct, err := h.a.PasswordSignIn(register, h.browserReq(hdr...), handle, password, func(token string, expires time.Time) {
		s = session{token: token, expires: expires, set: true}
	})
	return acct, s, err
}

// wantCode asserts err is a coded service error with code.
func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	var se *service.Error
	if !errors.As(err, &se) {
		t.Fatalf("err = %v (%T), want a coded error %s", err, err, code)
	}
	if se.Code != code {
		t.Fatalf("code = %q, want %q (%v)", se.Code, code, se)
	}
}

func TestPasswordRegisterAndLogin(t *testing.T) {
	h := pwHarness(t, nil)

	acct, s, err := h.signIn(true, "Speedy_Fox", goodPw)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if acct.Handle != goodHandle {
		t.Fatalf("handle = %q", acct.Handle)
	}
	if !s.set || len(s.token) < 20 {
		t.Fatal("no login cookie was set")
	}
	if _, ok := h.accounts.Authenticate(s.token); !ok {
		t.Fatal("the token is not a valid login")
	}

	// Logging in again rotates the token and ends the one the browser arrived with.
	first := s.token
	h.client.Jar.(*simpleJar).cookies[SessionCookie] = &http.Cookie{Name: SessionCookie, Value: first, Path: "/"}
	acct2, s2, err := h.signIn(false, "SPEEDY_FOX", goodPw)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if acct2.Handle != goodHandle {
		t.Fatalf("login handle = %q", acct2.Handle)
	}
	if s2.token == first {
		t.Fatal("the session token was reused (fixation)")
	}
	if _, ok := h.accounts.Authenticate(first); ok {
		t.Fatal("the previous login was not ended")
	}
}

func TestPasswordFailuresReadTheSame(t *testing.T) {
	h := pwHarness(t, nil)
	if _, _, err := h.signIn(true, goodHandle, goodPw); err != nil {
		t.Fatal(err)
	}
	// A wrong password and an unknown username must be indistinguishable.
	_, s1, errWrong := h.signIn(false, goodHandle, "tangerine-ladder-43")
	_, s2, errUnknown := h.signIn(false, "nobody_here", goodPw)
	wantCode(t, errWrong, service.CodeUnauthenticated)
	wantCode(t, errUnknown, service.CodeUnauthenticated)
	if errWrong.Error() != errUnknown.Error() {
		t.Fatalf("a wrong password and an unknown user are distinguishable: %v vs %v", errWrong, errUnknown)
	}
	if s1.set || s2.set {
		t.Fatal("a failed login set a cookie")
	}
}

func TestPasswordRegistrationValidation(t *testing.T) {
	h := pwHarness(t, nil)
	tests := []struct {
		name, handle, password string
	}{
		{"weak password", "fresh_one", "password123"},
		{"short password", "fresh_one", "short"},
		{"bad handle", "no spaces", goodPw},
		{"blocked handle", "fuck_you", goodPw},
		{"empty handle", "", goodPw},
		{"password is the username", "fresh_one", "Fresh_One"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, s, err := h.signIn(true, tc.handle, tc.password)
			if err == nil {
				t.Fatalf("register(%q, %q) succeeded", tc.handle, tc.password)
			}
			if s.set {
				t.Fatal("a refused registration set a cookie")
			}
		})
	}
	if _, _, err := h.signIn(true, goodHandle, goodPw); err != nil {
		t.Fatal(err)
	}
	h.client.Jar.(*simpleJar).cookies = map[string]*http.Cookie{}
	_, _, err := h.signIn(true, goodHandle, goodPw)
	wantCode(t, err, service.CodeInvalidArgument)
}

// Another site must be able neither to create accounts nor to guess passwords
// through a victim's browser, whichever transport asks.
func TestPasswordRefusesCrossSite(t *testing.T) {
	h := pwHarness(t, nil)
	if _, _, err := h.signIn(true, goodHandle, goodPw); err != nil {
		t.Fatal(err)
	}
	h.client.Jar.(*simpleJar).cookies = map[string]*http.Cookie{}

	for _, tc := range []struct {
		name string
		hdr  []string
	}{
		{"foreign origin", []string{"Origin", "https://evil.example"}},
		{"origin null", []string{"Origin", "null"}},
		{"cross-site metadata, no origin", []string{"Origin", "", "Sec-Fetch-Site", "cross-site"}},
	} {
		for _, register := range []bool{true, false} {
			t.Run(fmt.Sprint(tc.name, " register=", register), func(t *testing.T) {
				_, s, err := h.signIn(register, "fresh_one", goodPw, tc.hdr...)
				wantCode(t, err, service.CodeForbidden)
				if s.set {
					t.Fatal("a refused request set a cookie")
				}
			})
		}
	}
	if len(h.accounts.pw().byHandle) != 1 {
		t.Fatal("a cross-site request created an account")
	}
}

// The per-address request limit still guards the mutations.
func TestPasswordRateLimitedPerAddress(t *testing.T) {
	h := pwHarness(t, func(c *Config) { c.Limiter = NewLimiter(3, time.Minute, nil) })
	for i := 0; i < 3; i++ {
		if _, _, err := h.signIn(false, "nobody_here", goodPw); err == nil {
			t.Fatalf("attempt %d succeeded", i)
		}
	}
	_, s, err := h.signIn(false, "nobody_here", goodPw)
	wantCode(t, err, service.CodeLimit)
	if s.set {
		t.Fatal("a refused request set a cookie")
	}
}

func TestPasswordLockout(t *testing.T) {
	h := pwHarness(t, nil)
	if _, _, err := h.signIn(true, goodHandle, goodPw); err != nil {
		t.Fatal(err)
	}
	h.client.Jar.(*simpleJar).cookies = map[string]*http.Cookie{}

	for i := 0; i < 5; i++ {
		_, _, err := h.signIn(false, goodHandle, fmt.Sprintf("guess-number-%c", 'a'+i))
		wantCode(t, err, service.CodeUnauthenticated)
	}
	// Locked: even the right password is refused, without being checked.
	_, s, err := h.signIn(false, goodHandle, goodPw)
	wantCode(t, err, service.CodeLimit)
	if s.set {
		t.Fatal("a locked login set a cookie")
	}
	// A different client address is not locked by the per-address counter...
	if _, _, err := h.signIn(false, goodHandle, goodPw, "X-Forwarded-For", "203.0.113.50"); err != nil {
		t.Fatalf("other address: %v", err)
	}
	// ...and the lock lifts after 15 minutes.
	h.client.Jar.(*simpleJar).cookies = map[string]*http.Cookie{}
	h.advance(16 * time.Minute)
	if _, _, err := h.signIn(false, goodHandle, goodPw); err != nil {
		t.Fatalf("after the lock: %v", err)
	}
}

// Failures for one username from many addresses still lock that username.
func TestPasswordLockoutPerUsername(t *testing.T) {
	h := pwHarness(t, nil)
	if _, _, err := h.signIn(true, goodHandle, goodPw); err != nil {
		t.Fatal(err)
	}
	h.client.Jar.(*simpleJar).cookies = map[string]*http.Cookie{}
	for i := 0; i < 40; i++ {
		ip := fmt.Sprintf("198.51.100.%d", i+1)
		h.signIn(false, goodHandle, "wrong-guess-here", "X-Forwarded-For", ip)
	}
	_, _, err := h.signIn(false, goodHandle, goodPw, "X-Forwarded-For", "203.0.113.77")
	wantCode(t, err, service.CodeLimit)
}

func TestPasswordOffUnlessEnabled(t *testing.T) {
	g := newFakeGoogle(t)
	h := newHarness(t, func(string) []Provider { return []Provider{g.provider()} }, nil)
	for _, register := range []bool{true, false} {
		_, s, err := h.signIn(register, goodHandle, goodPw)
		wantCode(t, err, service.CodeForbidden)
		if s.set {
			t.Fatal("a refused request set a cookie")
		}
	}
	for _, p := range h.a.Providers() {
		if p.ID == PasswordProviderID {
			t.Fatal("password is offered while disabled")
		}
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

// Password sign-in has no registered callback URL, so it must work without
// -public-url: the same-origin check then trusts the request's own Host.
func TestPasswordSignInWithoutPublicURL(t *testing.T) {
	h := pwHarness(t, func(c *Config) { c.PublicURL = "" })

	if _, _, err := h.signIn(true, goodHandle, goodPw); err != nil {
		t.Fatalf("register without -public-url: %v", err)
	}
	// A cross-site Origin is still refused, with no -public-url to compare to.
	_, _, err := h.signIn(false, goodHandle, goodPw, "Origin", "https://evil.example")
	wantCode(t, err, service.CodeForbidden)
	// And a same-origin login still works.
	if _, _, err := h.signIn(false, goodHandle, goodPw); err != nil {
		t.Fatalf("login without -public-url: %v", err)
	}
}

// Without -public-url the request's Host is only trusted on local hosts. On a
// public host that resolves here (DNS rebinding), the page's own origin would
// otherwise match, so sign-in must be refused until -public-url is set.
func TestPasswordSignInWithoutPublicURLRefusesPublicHosts(t *testing.T) {
	h := pwHarness(t, func(c *Config) { c.PublicURL = "" })

	req := httptest.NewRequest(http.MethodPost, "http://rebind.evil.example/graphql", nil)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://rebind.evil.example")
	req.Host = "rebind.evil.example"
	req.RemoteAddr = "192.0.2.10:1234"
	set := false
	_, err := h.a.PasswordSignIn(true, req, goodHandle, goodPw, func(string, time.Time) { set = true })
	wantCode(t, err, service.CodeForbidden)
	if set {
		t.Fatal("a refused sign-in still set a cookie")
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
	if !PasswordFromEnv(env()) || !PasswordFromEnv(env("AUTH_PASSWORD", "")) || !PasswordFromEnv(env("AUTH_PASSWORD", "true")) {
		t.Fatal("password sign-in is not on by default")
	}
	for _, v := range []string{"false", "0", "no", "FALSE"} {
		if PasswordFromEnv(env("AUTH_PASSWORD", v)) {
			t.Errorf("AUTH_PASSWORD=%s did not turn password sign-in off", v)
		}
	}
	// A redirect provider still needs the public URL (its callback is registered).
	if _, err := ProvidersFromEnv(env("AUTH_GOOGLE_CLIENT_ID", "a", "AUTH_GOOGLE_CLIENT_SECRET", "b"), ""); err == nil || !strings.Contains(err.Error(), "-public-url") {
		t.Fatalf("err = %v", err)
	}
	// Password sign-in alone needs no callback URL, so it starts without one.
	if _, err := ProvidersFromEnv(env(), ""); err != nil {
		t.Fatalf("password-only sign-in needs -public-url: %v", err)
	}
	if _, err := ProvidersFromEnv(env("AUTH_GOOGLE_CLIENT_ID", "a", "AUTH_GOOGLE_CLIENT_SECRET", "b"), "https://x.example"); err != nil {
		t.Fatal(err)
	}
	if _, err := ProvidersFromEnv(env("AUTH_PASSWORD", "false", "AUTH_GOOGLE_CLIENT_ID", "a", "AUTH_GOOGLE_CLIENT_SECRET", "b"), ""); err == nil || !strings.Contains(err.Error(), "-public-url") {
		t.Fatalf("err = %v", err)
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
