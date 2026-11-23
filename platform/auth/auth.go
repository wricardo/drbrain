package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/wricardo/drbrain/platform/account"
	"github.com/wricardo/drbrain/platform/errs"
	"golang.org/x/oauth2"
)

const (
	// flowCookie binds an in-progress sign-in to the browser that started it.
	flowCookie = "drbrain_oauth"
	// flowTTL is how long a sign-in attempt may take; its state is single use.
	flowTTL = 10 * time.Minute
	// maxPending bounds the in-memory table of unfinished sign-ins.
	maxPending = 5000
	// exchangeTimeout bounds the calls to the provider during a callback.
	exchangeTimeout = 15 * time.Second
)

// Accounts is the part of the game service sign-in needs.
type Accounts interface {
	SignIn(provider, providerUserID string) (acct *account.Account, created bool, err error)
	// Lookup resolves the account named by an access token's subject.
	Lookup(id string) (account.Account, bool)
	RegisterPassword(handle, password string) (*account.Account, error)
	VerifyPassword(handle, password string) (*account.Account, error)
}

// PasswordProviderID is the id the UI sees for username/password sign-in. It is
// not a redirect provider: the form calls the registerWithPassword and
// logInWithPassword mutations instead of navigating anywhere.
const PasswordProviderID = account.PasswordProvider

// Config configures Auth.
type Config struct {
	Providers []Provider
	Accounts  Accounts
	// PublicURL is the site's base URL; callbacks are PublicURL/auth/<id>/callback
	// and an https URL makes cookies Secure.
	PublicURL string
	// AllowedOrigins is the ALLOWED_ORIGINS list ("" or "*" = only this site's own origin).
	AllowedOrigins string
	// Limiter guards the /auth routes (default 30 requests per minute per IP).
	Limiter *Limiter
	Now     func() time.Time
	// Password enables username/password sign-up and sign-in, next to or
	// instead of the OAuth providers. It is served by the GraphQL mutations
	// registerWithPassword and logInWithPassword (see PasswordSignIn).
	Password bool
	// BasePath is the public mount prefix, e.g. "/motor". Cookies and OAuth
	// callback URLs include it; route registration stays relative. Empty means
	// the game owns the site root.
	BasePath string
	// JWTSecret signs access tokens (HS256). Empty means a random per-process
	// key: fine for development, wrong for production and multiple instances.
	JWTSecret string
	// TokenTTL overrides DefaultTokenTTL.
	TokenTTL time.Duration
}

// Auth owns the sign-in routes, the OAuth flow cookie and the Bearer tokens
// issued after a successful sign-in.
type Auth struct {
	cfg       Config
	base      *url.URL
	basePath  string // "" or "/motor"; no trailing slash
	providers map[string]Provider
	order     []Provider
	allowed   map[string]bool // explicit allowed origins (without PublicURL's)
	limiter   *Limiter
	now       func() time.Time
	// Failed password logins: locked per (IP, username) and per username.
	failIP   *FailTracker
	failUser *FailTracker

	// Signing key for access tokens and the TTL they carry.
	jwtKey   []byte
	tokenTTL time.Duration

	mu      sync.Mutex
	pending map[string]pendingFlow
	// codes are one-time OAuth callback handoffs, keyed by their random value.
	codes map[string]pendingCode
}

// pendingCode is an OAuth sign-in that completed but whose tokens the browser
// has not fetched yet. Single use, short lived.
type pendingCode struct {
	accountID string
	expires   time.Time
}

type pendingFlow struct {
	provider string
	params   Params
	binding  string
	next     string
	expires  time.Time
}

// New validates cfg and builds Auth. At least one provider, or password
// sign-in, is required. -public-url is required by the redirect providers (their
// callbacks must be exact); password sign-in works without it, in which case the
// site's own origin comes from each request.
func New(cfg Config) (*Auth, error) {
	if (len(cfg.Providers) == 0 && !cfg.Password) || cfg.Accounts == nil {
		return nil, errors.New("auth: no providers configured")
	}
	base, err := ParseBaseURL(cfg.PublicURL)
	if err != nil {
		if cfg.PublicURL != "" || len(cfg.Providers) > 0 {
			return nil, err
		}
		base = nil
	}
	basePath := strings.TrimRight(strings.TrimSpace(cfg.BasePath), "/")
	if basePath != "" && !strings.HasPrefix(basePath, "/") {
		return nil, fmt.Errorf("auth: BasePath %q must be absolute", cfg.BasePath)
	}
	a := &Auth{
		cfg: cfg, base: base, basePath: basePath, providers: make(map[string]Provider), allowed: make(map[string]bool),
		limiter: cfg.Limiter, now: cfg.Now, pending: make(map[string]pendingFlow), codes: make(map[string]pendingCode),
		jwtKey: newJWTKey(cfg.JWTSecret), tokenTTL: cfg.TokenTTL,
	}
	if a.tokenTTL <= 0 {
		a.tokenTTL = DefaultTokenTTL
	}
	if a.now == nil {
		a.now = time.Now
	}
	if a.limiter == nil {
		a.limiter = NewLimiter(30, time.Minute, a.now)
	}
	a.failIP = NewFailTracker(5, 15*time.Minute, 15*time.Minute, a.now)
	a.failUser = NewFailTracker(40, time.Hour, 15*time.Minute, a.now)
	for _, p := range cfg.Providers {
		if _, dup := a.providers[p.ID()]; dup {
			return nil, errors.New("auth: duplicate provider " + p.ID())
		}
		a.providers[p.ID()] = p
		a.order = append(a.order, p)
	}
	if v := strings.TrimSpace(cfg.AllowedOrigins); v != "" && v != "*" {
		for _, o := range strings.Split(v, ",") {
			if o = strings.TrimRight(strings.TrimSpace(o), "/"); o != "" {
				a.allowed[o] = true
			}
		}
	}
	return a, nil
}

// ProviderInfo is what the UI needs to offer a sign-in button.
type ProviderInfo struct{ ID, Name string }

// Providers lists the enabled providers in configuration order.
func (a *Auth) Providers() []ProviderInfo {
	out := make([]ProviderInfo, len(a.order))
	for i, p := range a.order {
		out[i] = ProviderInfo{ID: p.ID(), Name: p.Name()}
	}
	if a.cfg.Password {
		out = append(out, ProviderInfo{ID: PasswordProviderID, Name: "Username and password"})
	}
	return out
}

// Routes mounts the sign-in routes on r. The redirect providers and the
// callback-code exchange need routes; password sign-in is served by the GraphQL
// mutations (see PasswordSignIn).
func (a *Auth) Routes(r *mux.Router) {
	if p, ok := a.providers["dev"].(*DevProvider); ok {
		r.HandleFunc("/auth/dev/authorize", a.guard(p.authorize)).Methods(http.MethodGet)
	}
	r.HandleFunc("/auth/{provider}/login", a.guard(a.handleLogin)).Methods(http.MethodGet)
	r.HandleFunc("/auth/{provider}/callback", a.guard(a.handleCallback)).Methods(http.MethodGet)
	r.HandleFunc("/auth/token", a.guard(a.handleToken)).Methods(http.MethodPost)
}

// Handler returns the sign-in routes as one handler mounted at the site root.
func (a *Auth) Handler() http.Handler {
	r := mux.NewRouter()
	a.Routes(r)
	return r
}

// guard adds no-store caching and the per-IP rate limit.
func (a *Auth) guard(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if ok, retry := a.limiter.Allow(ClientIP(r)); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(int(retry.Seconds())+1))
			http.Error(w, "too many sign-in requests; try again shortly", http.StatusTooManyRequests)
			return
		}
		h(w, r)
	}
}

func randomString(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// ---- cookies ---------------------------------------------------------------

// secure reports whether cookies get the Secure attribute: always for an https
// public URL, otherwise when the request itself came in over https.
func (a *Auth) secure(r *http.Request) bool {
	return (a.base != nil && a.base.Scheme == "https") || (r != nil && (r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"))
}

// flowPath scopes the in-progress sign-in cookie to the callback routes.
func (a *Auth) flowPath() string { return a.basePath + "/auth" }

func (a *Auth) setFlow(w http.ResponseWriter, r *http.Request, v string) {
	http.SetCookie(w, &http.Cookie{
		Name: flowCookie, Value: v, Path: a.flowPath(), MaxAge: int(flowTTL.Seconds()),
		HttpOnly: true, Secure: a.secure(r), SameSite: http.SameSiteLaxMode,
	})
}

func (a *Auth) clearFlow(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: flowCookie, Value: "", Path: a.flowPath(), MaxAge: -1,
		HttpOnly: true, Secure: a.secure(r), SameSite: http.SameSiteLaxMode,
	})
}

// ---- same-origin check -------------------------------------------------------

// originOf is the origin of the request's own site: PublicURL's, or, when the
// request carries a Host, scheme://host as the proxy reported it. The request's
// own origin is trusted when ALLOWED_ORIGINS is unset. With no PublicURL to
// compare against (password sign-in runs without -public-url) that fallback is
// limited to local hosts: otherwise a DNS-rebinding page, whose origin *is* the
// hostname it made resolve here, would match its own Origin. On a public host
// without -public-url or ALLOWED_ORIGINS the origin cannot be established, so
// state-changing requests are refused rather than trusted.
func (a *Auth) ownOrigins(r *http.Request) []string {
	var out []string
	if a.base != nil {
		out = append(out, a.base.Scheme+"://"+a.base.Host)
	}
	if r == nil {
		return out
	}
	if a.base == nil && !IsLocalHost(r.Host) {
		return out
	}
	if len(a.allowed) == 0 {
		scheme := "http"
		if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
			scheme = "https"
		}
		out = append(out, scheme+"://"+r.Host)
	}
	return out
}

// SameOrigin reports whether a state-changing request comes from this site (or
// an origin listed in ALLOWED_ORIGINS). Browsers always send Origin on
// cross-site POSTs, so a forged form or fetch from another site fails here;
// requests without Origin must at least not be marked cross-site by
// Sec-Fetch-Site. Cookies are never honoured for a request that fails this.
func (a *Auth) SameOrigin(r *http.Request) bool {
	site := r.Header.Get("Sec-Fetch-Site")
	if origin := r.Header.Get("Origin"); origin != "" {
		if a.allowed[strings.TrimRight(origin, "/")] {
			return true
		}
		for _, o := range a.ownOrigins(r) {
			if origin == o {
				return true
			}
		}
		return false
	}
	return site == "" || site == "same-origin" || site == "none"
}

// Viewer resolves the signed-in account of a request from its Authorization
// header ("Bearer <JWT>"). It returns nil for anonymous requests and for bad or
// expired tokens. A browser never adds this header by itself, so there is no
// cross-site request to reject here.
func (a *Auth) Viewer(r *http.Request) (*account.Account, string) {
	raw := BearerToken(r.Header.Get("Authorization"))
	if raw == "" {
		return nil, ""
	}
	acct, err := a.ParseToken(raw)
	if err != nil {
		return nil, ""
	}
	return acct, raw
}

// ---- routes ------------------------------------------------------------------

func (a *Auth) callbackURL(id string) string {
	return a.base.String() + a.basePath + "/auth/" + id + "/callback"
}

func (a *Auth) handleLogin(w http.ResponseWriter, r *http.Request) {
	p, ok := a.providers[mux.Vars(r)["provider"]]
	if !ok {
		http.NotFound(w, r)
		return
	}
	state, err1 := randomString(32)
	nonce, err2 := randomString(24)
	binding, err3 := randomString(24)
	if err := errors.Join(err1, err2, err3); err != nil {
		log.Printf("auth: random: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	params := Params{State: state, Nonce: nonce, Verifier: oauth2.GenerateVerifier(), RedirectURL: a.callbackURL(p.ID())}
	if !a.putPending(state, pendingFlow{
		provider: p.ID(), params: params, binding: binding, next: a.safeNext(r.URL.Query().Get("next")),
		expires: a.now().Add(flowTTL),
	}) {
		http.Error(w, "too many sign-ins in progress; try again shortly", http.StatusServiceUnavailable)
		return
	}
	a.setFlow(w, r, binding)
	http.Redirect(w, r, p.AuthURL(params), http.StatusFound)
}

// putPending stores an attempt, dropping expired ones; false when the table is full of live ones.
func (a *Auth) putPending(state string, f pendingFlow) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.pending) >= maxPending {
		now := a.now()
		for k, v := range a.pending {
			if !now.Before(v.expires) {
				delete(a.pending, k)
			}
		}
		if len(a.pending) >= maxPending {
			return false
		}
	}
	a.pending[state] = f
	return true
}

// takePending removes and returns an attempt: a state can be used exactly once.
func (a *Auth) takePending(state string) (pendingFlow, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	f, ok := a.pending[state]
	delete(a.pending, state)
	return f, ok && a.now().Before(f.expires)
}

// failRedirect sends the browser back to the site with a short reason.
func failRedirect(w http.ResponseWriter, r *http.Request, next, reason string) {
	http.Redirect(w, r, withQuery(next, "signin", reason), http.StatusFound)
}

func withQuery(path, key, val string) string {
	u, err := url.Parse(path)
	if err != nil {
		return "/?" + key + "=" + url.QueryEscape(val)
	}
	q := u.Query()
	q.Set(key, val)
	u.RawQuery = q.Encode()
	return u.String()
}

func (a *Auth) handleCallback(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["provider"]
	p, ok := a.providers[id]
	if !ok {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query()
	a.clearFlow(w, r)

	// The state is consumed first, whatever happens next: it works once.
	flow, ok := a.takePending(q.Get("state"))
	bind, err := r.Cookie(flowCookie)
	switch {
	case !ok || flow.provider != id:
		failRedirect(w, r, "/", "failed")
		return
	case err != nil || subtle.ConstantTimeCompare([]byte(bind.Value), []byte(flow.binding)) != 1:
		// A callback from a browser that did not start this sign-in (login CSRF).
		failRedirect(w, r, flow.next, "failed")
		return
	case q.Get("error") != "":
		failRedirect(w, r, flow.next, "cancelled")
		return
	case q.Get("code") == "":
		failRedirect(w, r, flow.next, "failed")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), exchangeTimeout)
	defer cancel()
	userID, err := p.Exchange(ctx, q.Get("code"), flow.params)
	if err != nil {
		log.Printf("auth: %s sign-in failed: %v", id, err)
		failRedirect(w, r, flow.next, "failed")
		return
	}
	acct, _, err := a.cfg.Accounts.SignIn(id, userID)
	if err != nil {
		log.Printf("auth: sign-in for %s: %v", id, err)
		failRedirect(w, r, flow.next, "failed")
		return
	}
	// The browser cannot see a redirect's headers, so hand it a single-use code
	// and let the SPA exchange it for a token (POST /auth/token).
	code, err := a.newCode(acct.ID)
	if err != nil {
		log.Printf("auth: handoff code: %v", err)
		failRedirect(w, r, flow.next, "failed")
		return
	}
	http.Redirect(w, r, withQuery(flow.next, "signin_code", code), http.StatusFound)
}

// ---- username / password -------------------------------------------------

// PasswordSignIn performs a username/password sign-up (register=true) or
// sign-in for the browser that made r, and returns the account plus a signed
// access token. The caller puts the token in the GraphQL response; nothing is
// stored server-side.
//
// This was an HTTP handler pair (/auth/password/register and /login) and is now
// called from the registerWithPassword and logInWithPassword mutations. The
// checks moved with it unchanged, so none of them depends on the route: a
// cross-site request is refused, requests are limited per address, and login
// failures are counted per (address, username) and per username, with a locked
// key refused before any hashing. The answer is the same for an unknown
// username and a wrong password.
func (a *Auth) PasswordSignIn(register bool, r *http.Request, handle, password string) (*account.Account, string, time.Time, error) {
	if !a.cfg.Password {
		return nil, "", time.Time{}, errs.Errorf(errs.CodeForbidden, "password sign-in is off")
	}
	if !a.SameOrigin(r) {
		return nil, "", time.Time{}, errs.Errorf(errs.CodeForbidden, "cross-site request refused")
	}
	if ok, retry := a.limiter.Allow(ClientIP(r)); !ok {
		return nil, "", time.Time{}, errs.Errorf(errs.CodeLimit, "too many sign-in requests; try again in %d seconds", int(retry.Seconds())+1)
	}
	normalized := account.NormalizeHandle(handle)
	ipKey := ClientIP(r) + "|" + normalized
	if !register {
		for _, t := range []struct {
			f   *FailTracker
			key string
		}{{a.failIP, ipKey}, {a.failUser, normalized}} {
			if blocked, wait := t.f.Blocked(t.key); blocked {
				return nil, "", time.Time{}, errs.Errorf(errs.CodeLimit,
					"too many failed attempts; try again in %d minutes", int(wait.Minutes())+1)
			}
		}
	}
	var acct *account.Account
	var err error
	if register {
		acct, err = a.cfg.Accounts.RegisterPassword(handle, password)
	} else {
		acct, err = a.cfg.Accounts.VerifyPassword(handle, password)
	}
	if err != nil {
		var se *errs.Error
		if errors.As(err, &se) {
			if se.Code == errs.CodeUnauthenticated {
				a.failIP.Fail(ipKey)
				a.failUser.Fail(normalized)
			}
			return nil, "", time.Time{}, err
		}
		log.Printf("auth: password %s: %v", map[bool]string{true: "register", false: "login"}[register], err)
		return nil, "", time.Time{}, errs.Errorf(errs.CodeInternal, "internal error")
	}
	a.failIP.Clear(ipKey)
	token, expires, err := a.IssueToken(acct.ID)
	if err != nil {
		log.Printf("auth: issue token: %v", err)
		return nil, "", time.Time{}, errs.Errorf(errs.CodeInternal, "internal error")
	}
	return acct, token, expires, nil
}

// ---- redirect validation -----------------------------------------------------

// SafeNext returns next if it is a same-site relative path ("/play/ab12?x=1"),
// otherwise "/". Absolute URLs, scheme-relative ("//evil"), backslash tricks,
// control characters and the /auth routes themselves are refused.
func SafeNext(next string) string {
	if next == "" || len(next) > 512 || next[0] != '/' || strings.HasPrefix(next, "//") {
		return "/"
	}
	for _, r := range next {
		if r < 0x20 || r == 0x7f || r == '\\' {
			return "/"
		}
	}
	u, err := url.Parse(next)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil || !strings.HasPrefix(u.Path, "/") || strings.HasPrefix(u.Path, "//") {
		return "/"
	}
	if u.Path == "/auth" || strings.HasPrefix(u.Path, "/auth/") {
		return "/"
	}
	return u.RequestURI()
}

// safeNext is SafeNext plus the game's own mount prefix: under /motor, a
// redirect back into /motor/auth/... is refused exactly like /auth/...
func (a *Auth) safeNext(next string) string {
	next = SafeNext(next)
	if a.basePath == "" {
		return next
	}
	authRoot := a.basePath + "/auth"
	if p, _, _ := strings.Cut(next, "?"); p == authRoot || strings.HasPrefix(p, authRoot+"/") {
		return "/"
	}
	return next
}
