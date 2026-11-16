package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/gorilla/mux"
	"github.com/wricardo/drbrain-motor-programming/game/service"
)

// ---- fake accounts ---------------------------------------------------------

type fakeAccounts struct {
	mu      sync.Mutex
	users   map[string]*service.Account // provider/user -> account
	logins  map[string]string           // token -> account id
	seq     int
	ended   []string
	started int
}

func newFakeAccounts() *fakeAccounts {
	return &fakeAccounts{users: map[string]*service.Account{}, logins: map[string]string{}}
}

func (f *fakeAccounts) SignIn(provider, user string) (*service.Account, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := provider + "/" + user
	if a, ok := f.users[k]; ok {
		return a, false, nil
	}
	f.seq++
	a := &service.Account{ID: fmt.Sprintf("acct%020d", f.seq), Provider: provider, ProviderUserID: user, Handle: fmt.Sprintf("user%d", f.seq)}
	f.users[k] = a
	return a, true, nil
}

func (f *fakeAccounts) StartLogin(id string) (string, time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started++
	tok := fmt.Sprintf("token-%d-%s-padding-padding", f.started, id)
	f.logins[tok] = id
	return tok, time.Now().Add(time.Hour), nil
}

func (f *fakeAccounts) Authenticate(tok string) (*service.Account, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, ok := f.logins[tok]
	if !ok {
		return nil, false
	}
	for _, a := range f.users {
		if a.ID == id {
			return a, true
		}
	}
	return nil, false
}

func (f *fakeAccounts) EndLogin(tok string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ended = append(f.ended, tok)
	delete(f.logins, tok)
}

// ---- fake Google -----------------------------------------------------------

// fakeGoogle is an OpenID Connect provider: an authorization endpoint is not
// needed (the test plays the browser), but the token and key endpoints are real
// HTTP, and the ID tokens are signed RS256.
type fakeGoogle struct {
	srv   *httptest.Server
	key   *rsa.PrivateKey
	mu    sync.Mutex
	sub   string
	aud   string
	nonce string // what the ID token will carry
	iss   string
	exp   time.Duration
	// seen is the last token request's form.
	seen url.Values
	// codes maps an issued code to the PKCE challenge it must be redeemed with.
	challenge map[string]string
}

func newFakeGoogle(t *testing.T) *fakeGoogle {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	g := &fakeGoogle{key: key, sub: "google-user-1", aud: "client-id", exp: time.Hour, challenge: map[string]string{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		jwk := jose.JSONWebKey{Key: &key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{jwk}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		g.mu.Lock()
		defer g.mu.Unlock()
		g.seen = r.PostForm
		if r.PostForm.Get("client_secret") != "client-secret" || r.PostForm.Get("code") != "good-code" || r.PostForm.Get("code_verifier") == "" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		signer, _ := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "k1"))
		iss := g.iss
		if iss == "" {
			iss = g.srv.URL
		}
		raw, _ := jwt.Signed(signer).Claims(jwt.Claims{
			Issuer: iss, Subject: g.sub, Audience: jwt.Audience{g.aud},
			Expiry: jwt.NewNumericDate(time.Now().Add(g.exp)), IssuedAt: jwt.NewNumericDate(time.Now()),
		}).Claims(map[string]any{"nonce": g.nonce}).Serialize()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "token_type": "Bearer", "id_token": raw})
	})
	g.srv = httptest.NewServer(mux)
	t.Cleanup(g.srv.Close)
	return g
}

func (g *fakeGoogle) provider() Provider {
	return NewGoogle(GoogleConfig{
		ClientID: "client-id", ClientSecret: "client-secret", Issuer: g.srv.URL,
		AuthURL: g.srv.URL + "/auth", TokenURL: g.srv.URL + "/token", JWKSURL: g.srv.URL + "/jwks",
	})
}

// ---- harness ---------------------------------------------------------------

type harness struct {
	t        *testing.T
	a        *Auth
	accounts *fakeAccounts
	ts       *httptest.Server
	client   *http.Client
	now      time.Time
	nowMu    sync.Mutex
}

// newHarness serves the /auth routes on a real httptest server whose own URL is
// the public URL; the client never follows redirects so each hop can be checked.
func newHarness(t *testing.T, providers func(base string) []Provider, mut func(*Config)) *harness {
	t.Helper()
	h := &harness{t: t, accounts: newFakeAccounts(), now: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)}
	r := mux.NewRouter()
	h.ts = httptest.NewUnstartedServer(r)
	h.ts.Start()
	t.Cleanup(h.ts.Close)
	cfg := Config{Providers: providers(h.ts.URL), Accounts: h.accounts, PublicURL: h.ts.URL, Now: func() time.Time {
		h.nowMu.Lock()
		defer h.nowMu.Unlock()
		return h.now
	}}
	if mut != nil {
		mut(&cfg)
	}
	a, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	h.a = a
	a.Routes(r)
	jar := &simpleJar{cookies: map[string]*http.Cookie{}}
	h.client = &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return h
}

func (h *harness) advance(d time.Duration) {
	h.nowMu.Lock()
	h.now = h.now.Add(d)
	h.nowMu.Unlock()
}

// simpleJar keeps cookies by name regardless of path (the test server is one host);
// paths are checked separately through the Set-Cookie headers.
type simpleJar struct {
	mu      sync.Mutex
	cookies map[string]*http.Cookie
}

func (j *simpleJar) SetCookies(_ *url.URL, cs []*http.Cookie) {
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, c := range cs {
		if c.MaxAge < 0 || c.Value == "" {
			delete(j.cookies, c.Name)
		} else {
			j.cookies[c.Name] = c
		}
	}
}

func (j *simpleJar) Cookies(u *url.URL) []*http.Cookie {
	j.mu.Lock()
	defer j.mu.Unlock()
	var out []*http.Cookie
	for _, c := range j.cookies {
		if c.Path == "/" || strings.HasPrefix(u.Path, c.Path) {
			out = append(out, c)
		}
	}
	return out
}

func (h *harness) cookie(name string) *http.Cookie {
	return h.client.Jar.(*simpleJar).cookies[name]
}

func (h *harness) get(path string, hdr ...string) *http.Response {
	h.t.Helper()
	req, _ := http.NewRequest(http.MethodGet, h.ts.URL+path, nil)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := h.client.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

// startLogin begins a sign-in and returns the provider URL the browser is sent to.
func (h *harness) startLogin(provider, next string) *url.URL {
	h.t.Helper()
	path := "/auth/" + provider + "/login"
	if next != "" {
		path += "?next=" + url.QueryEscape(next)
	}
	resp := h.get(path)
	if resp.StatusCode != http.StatusFound {
		h.t.Fatalf("login status = %d", resp.StatusCode)
	}
	u, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		h.t.Fatal(err)
	}
	return u
}

func setCookieHeader(resp *http.Response, name string) string {
	for _, v := range resp.Header.Values("Set-Cookie") {
		if strings.HasPrefix(v, name+"=") {
			return v
		}
	}
	return ""
}

// ---- tests -----------------------------------------------------------------

func TestGoogleFlowSucceeds(t *testing.T) {
	g := newFakeGoogle(t)
	h := newHarness(t, func(string) []Provider { return []Provider{g.provider()} }, nil)

	authURL := h.startLogin("google", "/play/abc")
	q := authURL.Query()
	// The authorization request carries state, nonce, a PKCE S256 challenge, the
	// registered callback and only the openid scope.
	if q.Get("state") == "" || q.Get("nonce") == "" || q.Get("code_challenge") == "" || q.Get("code_challenge_method") != "S256" {
		t.Fatalf("auth URL missing security parameters: %v", q)
	}
	if q.Get("redirect_uri") != h.ts.URL+"/auth/google/callback" || q.Get("scope") != "openid" || q.Get("response_type") != "code" || q.Get("client_id") != "client-id" {
		t.Fatalf("auth URL = %v", q)
	}
	flow := h.cookie(flowCookie)
	if flow == nil || flow.Path != "/auth" || !flow.HttpOnly {
		t.Fatalf("flow cookie = %+v", flow)
	}
	g.nonce = q.Get("nonce")

	resp := h.get("/auth/google/callback?code=good-code&state=" + url.QueryEscape(q.Get("state")))
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/play/abc" {
		t.Fatalf("callback = %d -> %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if g.seen.Get("code_verifier") == "" || g.seen.Get("redirect_uri") != h.ts.URL+"/auth/google/callback" {
		t.Fatalf("token request = %v", g.seen)
	}
	tok := h.cookie(SessionCookie)
	if tok == nil || tok.Value == "" {
		t.Fatal("no session cookie")
	}
	if acct, ok := h.accounts.Authenticate(tok.Value); !ok || acct.ProviderUserID != "google-user-1" || acct.Provider != "google" {
		t.Fatalf("login not tied to the provider subject: %+v", acct)
	}
	if h.cookie(flowCookie) != nil {
		t.Fatal("flow cookie not cleared")
	}
}

func TestCallbackRejections(t *testing.T) {
	g := newFakeGoogle(t)
	type step struct {
		name  string
		setup func(h *harness, q url.Values) (callback string, hdr []string)
	}
	steps := []step{
		{"unknown state", func(h *harness, q url.Values) (string, []string) {
			return "/auth/google/callback?code=good-code&state=nope", nil
		}},
		{"missing state", func(h *harness, q url.Values) (string, []string) {
			return "/auth/google/callback?code=good-code", nil
		}},
		{"missing code", func(h *harness, q url.Values) (string, []string) {
			return "/auth/google/callback?state=" + url.QueryEscape(q.Get("state")), nil
		}},
		{"bad code", func(h *harness, q url.Values) (string, []string) {
			g.nonce = q.Get("nonce")
			return "/auth/google/callback?code=wrong&state=" + url.QueryEscape(q.Get("state")), nil
		}},
		{"nonce mismatch", func(h *harness, q url.Values) (string, []string) {
			g.nonce = "attacker-nonce"
			return "/auth/google/callback?code=good-code&state=" + url.QueryEscape(q.Get("state")), nil
		}},
		{"provider denied", func(h *harness, q url.Values) (string, []string) {
			return "/auth/google/callback?error=access_denied&state=" + url.QueryEscape(q.Get("state")), nil
		}},
		{"wrong audience", func(h *harness, q url.Values) (string, []string) {
			g.nonce, g.aud = q.Get("nonce"), "someone-elses-client"
			return "/auth/google/callback?code=good-code&state=" + url.QueryEscape(q.Get("state")), nil
		}},
		{"wrong issuer", func(h *harness, q url.Values) (string, []string) {
			g.nonce, g.iss = q.Get("nonce"), "https://evil.example"
			return "/auth/google/callback?code=good-code&state=" + url.QueryEscape(q.Get("state")), nil
		}},
		{"expired id token", func(h *harness, q url.Values) (string, []string) {
			g.nonce, g.exp = q.Get("nonce"), -time.Hour
			return "/auth/google/callback?code=good-code&state=" + url.QueryEscape(q.Get("state")), nil
		}},
	}
	for _, st := range steps {
		t.Run(st.name, func(t *testing.T) {
			g.mu.Lock()
			g.aud, g.iss, g.exp, g.nonce = "client-id", "", time.Hour, ""
			g.mu.Unlock()
			h := newHarness(t, func(string) []Provider { return []Provider{g.provider()} }, nil)
			q := h.startLogin("google", "/maps").Query()
			cb, hdr := st.setup(h, q)
			resp := h.get(cb, hdr...)
			if resp.StatusCode != http.StatusFound || !strings.Contains(resp.Header.Get("Location"), "signin=") {
				t.Fatalf("callback = %d -> %q, want a redirect with signin=...", resp.StatusCode, resp.Header.Get("Location"))
			}
			if h.cookie(SessionCookie) != nil || h.accounts.started != 0 {
				t.Fatal("a login was created for a rejected sign-in")
			}
		})
	}
}

func TestStateIsSingleUse(t *testing.T) {
	g := newFakeGoogle(t)
	h := newHarness(t, func(string) []Provider { return []Provider{g.provider()} }, nil)
	q := h.startLogin("google", "").Query()
	g.nonce = q.Get("nonce")
	cb := "/auth/google/callback?code=good-code&state=" + url.QueryEscape(q.Get("state"))
	if resp := h.get(cb); strings.Contains(resp.Header.Get("Location"), "signin=") {
		t.Fatalf("first use failed: %s", resp.Header.Get("Location"))
	}
	n := h.accounts.started
	// Replaying the very same callback (with the flow cookie restored) must fail.
	h.client.Jar.(*simpleJar).cookies[flowCookie] = &http.Cookie{Name: flowCookie, Value: "x", Path: "/auth"}
	resp := h.get(cb)
	if !strings.Contains(resp.Header.Get("Location"), "signin=failed") || h.accounts.started != n {
		t.Fatalf("replay = %q (logins %d -> %d)", resp.Header.Get("Location"), n, h.accounts.started)
	}
}

// Login CSRF: an attacker starts a sign-in, then tricks the victim's browser into
// completing the attacker's callback. The victim's browser has no matching flow cookie.
func TestCallbackFromAnotherBrowserIsRejected(t *testing.T) {
	g := newFakeGoogle(t)
	h := newHarness(t, func(string) []Provider { return []Provider{g.provider()} }, nil)
	q := h.startLogin("google", "").Query()
	g.nonce = q.Get("nonce")
	delete(h.client.Jar.(*simpleJar).cookies, flowCookie) // the victim never started this flow
	resp := h.get("/auth/google/callback?code=good-code&state=" + url.QueryEscape(q.Get("state")))
	if !strings.Contains(resp.Header.Get("Location"), "signin=failed") || h.cookie(SessionCookie) != nil {
		t.Fatalf("foreign callback accepted: %q", resp.Header.Get("Location"))
	}
	// And with a wrong (but present) cookie.
	q = h.startLogin("google", "").Query()
	g.nonce = q.Get("nonce")
	h.client.Jar.(*simpleJar).cookies[flowCookie].Value = "somebody-elses-binding"
	resp = h.get("/auth/google/callback?code=good-code&state=" + url.QueryEscape(q.Get("state")))
	if !strings.Contains(resp.Header.Get("Location"), "signin=failed") || h.cookie(SessionCookie) != nil {
		t.Fatalf("wrong binding accepted: %q", resp.Header.Get("Location"))
	}
}

func TestStateExpires(t *testing.T) {
	g := newFakeGoogle(t)
	h := newHarness(t, func(string) []Provider { return []Provider{g.provider()} }, nil)
	q := h.startLogin("google", "").Query()
	g.nonce = q.Get("nonce")
	h.advance(flowTTL + time.Second)
	resp := h.get("/auth/google/callback?code=good-code&state=" + url.QueryEscape(q.Get("state")))
	if !strings.Contains(resp.Header.Get("Location"), "signin=failed") {
		t.Fatalf("expired state accepted: %q", resp.Header.Get("Location"))
	}
}

func TestStateCannotCrossProviders(t *testing.T) {
	g := newFakeGoogle(t)
	h := newHarness(t, func(base string) []Provider { return []Provider{g.provider(), NewDev(base)} }, nil)
	q := h.startLogin("google", "").Query()
	resp := h.get("/auth/dev/callback?code=alice&state=" + url.QueryEscape(q.Get("state")))
	if !strings.Contains(resp.Header.Get("Location"), "signin=failed") || h.cookie(SessionCookie) != nil {
		t.Fatalf("a google state completed a dev sign-in: %q", resp.Header.Get("Location"))
	}
}

func TestSignInRotatesExistingSession(t *testing.T) {
	g := newFakeGoogle(t)
	h := newHarness(t, func(string) []Provider { return []Provider{g.provider()} }, nil)
	old := "planted-token-planted-token-planted"
	h.client.Jar.(*simpleJar).cookies[SessionCookie] = &http.Cookie{Name: SessionCookie, Value: old, Path: "/"}
	h.accounts.logins[old] = "someone"

	q := h.startLogin("google", "").Query()
	g.nonce = q.Get("nonce")
	h.get("/auth/google/callback?code=good-code&state=" + url.QueryEscape(q.Get("state")))
	fresh := h.cookie(SessionCookie)
	if fresh == nil || fresh.Value == old {
		t.Fatal("the pre-existing session token was kept (session fixation)")
	}
	if _, ok := h.accounts.logins[old]; ok {
		t.Fatal("the old login was not ended")
	}
}

func TestCookieFlags(t *testing.T) {
	g := newFakeGoogle(t)
	signedIn := func(t *testing.T, h *harness, hdr ...string) *http.Response {
		q := h.startLogin("google", "").Query()
		g.nonce = q.Get("nonce")
		return h.get("/auth/google/callback?code=good-code&state="+url.QueryEscape(q.Get("state")), hdr...)
	}
	t.Run("plain http localhost: not Secure", func(t *testing.T) {
		h := newHarness(t, func(string) []Provider { return []Provider{g.provider()} }, nil)
		c := setCookieHeader(signedIn(t, h), SessionCookie)
		for _, want := range []string{"HttpOnly", "SameSite=Lax", "Path=/", "Max-Age="} {
			if !strings.Contains(c, want) {
				t.Errorf("cookie %q lacks %s", c, want)
			}
		}
		if strings.Contains(c, "Secure") {
			t.Errorf("cookie %q is Secure on plain http", c)
		}
	})
	t.Run("behind a TLS proxy: Secure", func(t *testing.T) {
		h := newHarness(t, func(string) []Provider { return []Provider{g.provider()} }, nil)
		c := setCookieHeader(signedIn(t, h, "X-Forwarded-Proto", "https"), SessionCookie)
		if !strings.Contains(c, "Secure") || !strings.Contains(c, "HttpOnly") || !strings.Contains(c, "SameSite=Lax") {
			t.Errorf("cookie = %q", c)
		}
	})
	t.Run("https public URL: always Secure", func(t *testing.T) {
		a, err := New(Config{Providers: []Provider{g.provider()}, Accounts: newFakeAccounts(), PublicURL: "https://example.com"})
		if err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		a.SetSession(rec, httptest.NewRequest("GET", "http://example.com/", nil), "tok", time.Now().Add(time.Hour))
		if c := rec.Header().Get("Set-Cookie"); !strings.Contains(c, "Secure") {
			t.Errorf("cookie = %q", c)
		}
	})
	t.Run("flow cookie", func(t *testing.T) {
		h := newHarness(t, func(string) []Provider { return []Provider{g.provider()} }, nil)
		resp := h.get("/auth/google/login")
		c := setCookieHeader(resp, flowCookie)
		for _, want := range []string{"HttpOnly", "SameSite=Lax", "Path=/auth", "Max-Age=600"} {
			if !strings.Contains(c, want) {
				t.Errorf("flow cookie %q lacks %s", c, want)
			}
		}
	})
}

func TestUnknownProviderAndMethods(t *testing.T) {
	g := newFakeGoogle(t)
	h := newHarness(t, func(string) []Provider { return []Provider{g.provider()} }, nil)
	for _, p := range []string{"/auth/github/login", "/auth/github/callback", "/auth/dev/authorize", "/auth/../x/login"} {
		if resp := h.get(p); resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusMovedPermanently {
			t.Errorf("GET %s = %d, want 404", p, resp.StatusCode)
		}
	}
	// The password routes and the logout route are gone: signing in and out are
	// GraphQL mutations now (see TestPasswordRegisterAndLogin in password_test.go
	// and the api package for the mutations over HTTP). A POST to the old
	// password paths only matches the {provider} GET routes, so it is 405.
	for _, p := range []string{"/auth/logout", "/auth/password/register", "/auth/password/login"} {
		for _, m := range []string{http.MethodGet, http.MethodPost} {
			req, _ := http.NewRequest(m, h.ts.URL+p, nil)
			resp, err := h.client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusMethodNotAllowed {
				t.Errorf("%s %s = %d, want 404", m, p, resp.StatusCode)
			}
			if len(resp.Header.Values("Set-Cookie")) != 0 {
				t.Errorf("%s %s set a cookie", m, p)
			}
		}
	}
}

func TestRateLimitOnAuthRoutes(t *testing.T) {
	g := newFakeGoogle(t)
	h := newHarness(t, func(string) []Provider { return []Provider{g.provider()} }, func(c *Config) {
		c.Limiter = NewLimiter(3, time.Minute, nil)
	})
	for i := 0; i < 3; i++ {
		if resp := h.get("/auth/google/login"); resp.StatusCode != http.StatusFound {
			t.Fatalf("request %d = %d", i, resp.StatusCode)
		}
	}
	resp := h.get("/auth/google/login")
	if resp.StatusCode != http.StatusTooManyRequests || resp.Header.Get("Retry-After") == "" {
		t.Fatalf("over the limit = %d (Retry-After %q)", resp.StatusCode, resp.Header.Get("Retry-After"))
	}
	// Another client address is not affected (loopback proxy header).
	resp = h.get("/auth/google/login", "X-Forwarded-For", "203.0.113.9")
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("other IP limited too: %d", resp.StatusCode)
	}
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("auth responses must not be cached")
	}
}

func TestPendingTableIsBounded(t *testing.T) {
	g := newFakeGoogle(t)
	h := newHarness(t, func(string) []Provider { return []Provider{g.provider()} }, func(c *Config) {
		c.Limiter = NewLimiter(1<<30, time.Minute, nil)
	})
	for i := 0; i < maxPending; i++ {
		h.a.pending[fmt.Sprint("s", i)] = pendingFlow{expires: h.now.Add(time.Hour)}
	}
	if resp := h.get("/auth/google/login"); resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("full table = %d", resp.StatusCode)
	}
	h.advance(2 * time.Hour) // everything expires; the table frees itself
	if resp := h.get("/auth/google/login"); resp.StatusCode != http.StatusFound {
		t.Fatalf("after expiry = %d", resp.StatusCode)
	}
}

func TestSameOriginAndViewer(t *testing.T) {
	g := newFakeGoogle(t)
	mk := func(allowed string) *Auth {
		a, err := New(Config{Providers: []Provider{g.provider()}, Accounts: newFakeAccounts(), PublicURL: "https://motor.example.com", AllowedOrigins: allowed})
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	req := func(method, origin, site, ctype string) *http.Request {
		r := httptest.NewRequest(method, "https://motor.example.com/graphql", nil)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if site != "" {
			r.Header.Set("Sec-Fetch-Site", site)
		}
		if ctype != "" {
			r.Header.Set("Content-Type", ctype)
		}
		return r
	}
	tests := []struct {
		name    string
		allowed string
		r       *http.Request
		want    bool
	}{
		{"same origin", "", req("POST", "https://motor.example.com", "same-origin", ""), true},
		{"other site", "", req("POST", "https://evil.example", "cross-site", ""), false},
		{"other site, no fetch metadata", "", req("POST", "https://evil.example", "", ""), false},
		{"same host, other scheme", "", req("POST", "http://motor.example.com", "", ""), false},
		{"no origin, no metadata (non-browser)", "", req("POST", "", "", ""), true},
		{"no origin, cross-site metadata", "", req("POST", "", "cross-site", ""), false},
		{"no origin, same-site metadata", "", req("POST", "", "same-site", ""), false},
		{"user navigation", "", req("POST", "", "none", ""), true},
		{"allow-listed origin", "http://localhost:5173", req("POST", "http://localhost:5173", "same-site", ""), true},
		{"allow-list excludes unlisted", "http://localhost:5173", req("POST", "https://evil.example", "", ""), false},
		{"allow-list still allows the public URL", "http://localhost:5173", req("POST", "https://motor.example.com", "", ""), true},
		{"wildcard list means own origin only", "*", req("POST", "https://evil.example", "", ""), false},
		{"null origin", "", req("POST", "null", "", ""), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := mk(tc.allowed).SameOrigin(tc.r); got != tc.want {
				t.Fatalf("SameOrigin = %v, want %v", got, tc.want)
			}
		})
	}

	t.Run("viewer ignores the cookie unless same-origin JSON", func(t *testing.T) {
		accts := newFakeAccounts()
		a, _ := New(Config{Providers: []Provider{g.provider()}, Accounts: accts, PublicURL: "https://motor.example.com"})
		acct, _, _ := accts.SignIn("google", "u")
		tok, _, _ := accts.StartLogin(acct.ID)
		with := func(r *http.Request) *http.Request {
			r.AddCookie(&http.Cookie{Name: SessionCookie, Value: tok})
			return r
		}
		cases := []struct {
			name string
			r    *http.Request
			want bool
		}{
			{"same-origin json", with(req("POST", "https://motor.example.com", "same-origin", "application/json")), true},
			{"same-origin json with charset", with(req("POST", "https://motor.example.com", "", "application/json; charset=utf-8")), true},
			{"cross-site", with(req("POST", "https://evil.example", "cross-site", "application/json")), false},
			{"form post (simple request)", with(req("POST", "https://motor.example.com", "same-origin", "application/x-www-form-urlencoded")), false},
			{"text/plain post", with(req("POST", "https://motor.example.com", "same-origin", "text/plain")), false},
			{"no content type", with(req("POST", "https://motor.example.com", "same-origin", "")), false},
			{"no cookie", req("POST", "https://motor.example.com", "same-origin", "application/json"), false},
		}
		for _, tc := range cases {
			got, _ := a.Viewer(tc.r)
			if (got != nil) != tc.want {
				t.Errorf("%s: viewer = %v, want signed in = %v", tc.name, got, tc.want)
			}
		}
		bad := with(req("POST", "https://motor.example.com", "same-origin", "application/json"))
		bad.Header.Set("Cookie", SessionCookie+"=not-a-real-token-not-a-real-token")
		if got, _ := a.Viewer(bad); got != nil {
			t.Error("an unknown token authenticated")
		}
	})
}

func TestSafeNext(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", "/"},
		{"/", "/"},
		{"/play/abc123", "/play/abc123"},
		{"/leaderboard?map=a&order=steps", "/leaderboard?map=a&order=steps"},
		{"/u/otter", "/u/otter"},
		{"https://evil.example/", "/"},
		{"//evil.example/path", "/"},
		{"///evil.example", "/"},
		{"/\\evil.example", "/"},
		{"\\\\evil.example", "/"},
		{"javascript:alert(1)", "/"},
		{"evil.example", "/"},
		{"/ok\r\nSet-Cookie: x=y", "/"},
		{"/ok\x00", "/"},
		{"/%2f%2fevil.example", "/"}, // decodes to //evil.example: refused
		{"/auth/google/login", "/"},
		{"/auth", "/"},
		{"/authors", "/authors"},
		{"/" + strings.Repeat("a", 600), "/"},
		{"/path with space", "/path%20with%20space"},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			if got := SafeNext(tc.in); got != tc.want {
				t.Fatalf("SafeNext(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestLimiter(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	l := NewLimiter(2, time.Minute, func() time.Time { return now })
	for i := 0; i < 2; i++ {
		if ok, _ := l.Allow("a"); !ok {
			t.Fatalf("request %d refused", i)
		}
	}
	ok, retry := l.Allow("a")
	if ok || retry <= 0 || retry > time.Minute {
		t.Fatalf("third = %v, retry %v", ok, retry)
	}
	if ok, _ := l.Allow("b"); !ok {
		t.Fatal("keys share a bucket")
	}
	now = now.Add(time.Minute + time.Second)
	if ok, _ := l.Allow("a"); !ok {
		t.Fatal("window did not reset")
	}
	// Bounded memory: a full table of live buckets refuses new keys.
	l2 := NewLimiter(1, time.Hour, func() time.Time { return now })
	for i := 0; i < maxBuckets; i++ {
		l2.Allow(fmt.Sprint(i))
	}
	if ok, _ := l2.Allow("one-more"); ok {
		t.Fatal("table grew past its bound")
	}
}

func TestClientIP(t *testing.T) {
	mk := func(remote, xff string) *http.Request {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = remote
		if xff != "" {
			r.Header.Set("X-Forwarded-For", xff)
		}
		return r
	}
	tests := []struct {
		name string
		r    *http.Request
		want string
	}{
		{"direct", mk("198.51.100.7:1234", ""), "198.51.100.7"},
		{"direct ignores a spoofed header", mk("198.51.100.7:1234", "1.2.3.4"), "198.51.100.7"},
		{"proxy on loopback uses the last entry", mk("127.0.0.1:5555", "6.6.6.6, 203.0.113.9"), "203.0.113.9"},
		{"proxy ipv6 loopback", mk("[::1]:5555", "203.0.113.9"), "203.0.113.9"},
		{"proxy without header", mk("127.0.0.1:5555", ""), "127.0.0.1"},
		{"proxy with garbage", mk("127.0.0.1:5555", "not-an-ip"), "127.0.0.1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClientIP(tc.r); got != tc.want {
				t.Fatalf("ClientIP = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestGitHubFlow(t *testing.T) {
	var gotAuth string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login/oauth/access_token":
			_ = r.ParseForm()
			if r.PostForm.Get("code") != "gh-code" || r.PostForm.Get("code_verifier") == "" || r.PostForm.Get("client_secret") != "gh-secret" {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"error":"bad_verification_code"}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"gho_token","token_type":"bearer"}`))
		case "/user":
			gotAuth = r.Header.Get("Authorization")
			_, _ = w.Write([]byte(`{"id": 4242, "login": "octocat", "email": "must-not-be-read@example.com", "avatar_url": "x"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()
	h := newHarness(t, func(string) []Provider {
		return []Provider{NewGitHub(GitHubConfig{
			ClientID: "gh-id", ClientSecret: "gh-secret", AuthURL: api.URL + "/login/oauth/authorize",
			TokenURL: api.URL + "/login/oauth/access_token", APIURL: api.URL,
		})}
	}, nil)

	u := h.startLogin("github", "/maps")
	q := u.Query()
	if !strings.HasSuffix(u.Path, "/login/oauth/authorize") || q.Get("state") == "" || q.Get("code_challenge_method") != "S256" || q.Get("scope") != "" {
		t.Fatalf("github auth URL = %v", u)
	}
	resp := h.get("/auth/github/callback?code=gh-code&state=" + url.QueryEscape(q.Get("state")))
	if resp.Header.Get("Location") != "/maps" {
		t.Fatalf("callback -> %q", resp.Header.Get("Location"))
	}
	acct, ok := h.accounts.Authenticate(h.cookie(SessionCookie).Value)
	if !ok || acct.Provider != "github" || acct.ProviderUserID != "4242" {
		t.Fatalf("account = %+v", acct)
	}
	if gotAuth != "Bearer gho_token" {
		t.Fatalf("user request auth = %q", gotAuth)
	}

	// A failing token exchange is a failed sign-in, not a crash.
	q = h.startLogin("github", "").Query()
	resp = h.get("/auth/github/callback?code=other&state=" + url.QueryEscape(q.Get("state")))
	if !strings.Contains(resp.Header.Get("Location"), "signin=failed") {
		t.Fatalf("bad code -> %q", resp.Header.Get("Location"))
	}
}

func TestDevProvider(t *testing.T) {
	h := newHarness(t, func(base string) []Provider { return []Provider{NewDev(base)} }, nil)
	u := h.startLogin("dev", "/runs")
	if u.Path != "/auth/dev/authorize" {
		t.Fatalf("dev auth URL = %v", u)
	}
	state := u.Query().Get("state")
	req, _ := http.NewRequest("GET", h.ts.URL+u.RequestURI(), nil)
	page, err := h.client.Do(req)
	if err != nil || page.StatusCode != http.StatusOK {
		t.Fatalf("authorize page: %v %v", err, page)
	}
	page.Body.Close()
	resp := h.get("/auth/dev/callback?code=alice&state=" + url.QueryEscape(state))
	if resp.Header.Get("Location") != "/runs" {
		t.Fatalf("callback -> %q", resp.Header.Get("Location"))
	}
	if a, ok := h.accounts.Authenticate(h.cookie(SessionCookie).Value); !ok || a.ProviderUserID != "alice" {
		t.Fatalf("account = %+v", a)
	}
	// Odd user names are refused.
	q := h.startLogin("dev", "").Query()
	resp = h.get("/auth/dev/callback?code=" + url.QueryEscape("../Evil User") + "&state=" + url.QueryEscape(q.Get("state")))
	if !strings.Contains(resp.Header.Get("Location"), "signin=failed") {
		t.Fatalf("odd dev user accepted: %q", resp.Header.Get("Location"))
	}
	// The sign-in page itself refuses non-local hosts.
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "http://motor.example.com/auth/dev/authorize?state=x", nil)
	NewDev("http://localhost:8000").authorize(rec, r)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("authorize on a public host = %d", rec.Code)
	}
}

func TestProvidersFromEnv(t *testing.T) {
	env := func(kv ...string) func(string) string {
		m := map[string]string{}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return func(k string) string { return m[k] }
	}
	tests := []struct {
		name      string
		env       func(string) string
		publicURL string
		wantIDs   []string
		wantErr   string
	}{
		{"nothing configured: sign-in off, no URL needed", env(), "", nil, ""},
		{"nothing configured with a URL", env(), "https://x.example", nil, ""},
		{"google only", env("AUTH_GOOGLE_CLIENT_ID", "a", "AUTH_GOOGLE_CLIENT_SECRET", "b"), "https://x.example", []string{"google"}, ""},
		{"both, in a stable order", env("AUTH_GITHUB_CLIENT_ID", "a", "AUTH_GITHUB_CLIENT_SECRET", "b", "AUTH_GOOGLE_CLIENT_ID", "a", "AUTH_GOOGLE_CLIENT_SECRET", "b"), "https://x.example/", []string{"google", "github"}, ""},
		{"id without secret", env("AUTH_GOOGLE_CLIENT_ID", "a"), "https://x.example", nil, "AUTH_GOOGLE_CLIENT_ID and AUTH_GOOGLE_CLIENT_SECRET must both be set"},
		{"secret without id", env("AUTH_GITHUB_CLIENT_SECRET", "b"), "https://x.example", nil, "AUTH_GITHUB"},
		{"provider without public URL", env("AUTH_GOOGLE_CLIENT_ID", "a", "AUTH_GOOGLE_CLIENT_SECRET", "b"), "", nil, "-public-url"},
		{"public URL with a path", env("AUTH_GOOGLE_CLIENT_ID", "a", "AUTH_GOOGLE_CLIENT_SECRET", "b"), "https://x.example/app", nil, "-public-url"},
		{"dev fake on localhost", env("AUTH_DEV_FAKE", "true"), "http://localhost:8000", []string{"dev"}, ""},
		{"dev fake on 127.0.0.1", env("AUTH_DEV_FAKE", "true"), "http://127.0.0.1:8000", []string{"dev"}, ""},
		{"dev fake on a public host: refuse to start", env("AUTH_DEV_FAKE", "true"), "https://motor-programming.wricardo.net", nil, "AUTH_DEV_FAKE"},
		{"dev fake with no public URL: refuse to start", env("AUTH_DEV_FAKE", "true"), "", nil, "AUTH_DEV_FAKE"},
		{"dev fake on a lookalike host", env("AUTH_DEV_FAKE", "true"), "http://localhost.evil.example", nil, "AUTH_DEV_FAKE"},
		{"dev fake off", env("AUTH_DEV_FAKE", "false"), "https://x.example", nil, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ps, err := ProvidersFromEnv(tc.env, tc.publicURL)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var ids []string
			for _, p := range ps {
				ids = append(ids, p.ID())
			}
			if strings.Join(ids, ",") != strings.Join(tc.wantIDs, ",") {
				t.Fatalf("providers = %v, want %v", ids, tc.wantIDs)
			}
		})
	}
}

func TestIsLocalHost(t *testing.T) {
	for host, want := range map[string]bool{
		"localhost": true, "localhost:8000": true, "127.0.0.1:9": true, "[::1]:8000": true, "LOCALHOST": true,
		"example.com": false, "localhost.evil.example": false, "127.0.0.1.evil.example": false, "10.0.0.1": false, "0.0.0.0:8000": false, "": false,
	} {
		if got := IsLocalHost(host); got != want {
			t.Errorf("IsLocalHost(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestNewRequiresProviders(t *testing.T) {
	if _, err := New(Config{Accounts: newFakeAccounts(), PublicURL: "http://localhost"}); err == nil {
		t.Fatal("New without providers succeeded")
	}
	g := newFakeGoogle(t)
	if _, err := New(Config{Providers: []Provider{g.provider(), g.provider()}, Accounts: newFakeAccounts(), PublicURL: "http://localhost"}); err == nil {
		t.Fatal("duplicate providers accepted")
	}
}
