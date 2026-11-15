// Package auth implements optional player sign-in: OAuth2 / OpenID Connect
// login with Google and GitHub through a small Provider interface, the HTTP
// routes under /auth, the login cookie, same-origin (CSRF) checks and a
// per-IP rate limiter. Accounts and logins themselves live in game/service.
//
// Sign-in is off unless at least one provider has credentials in the
// environment; nothing in this package is mounted otherwise.
package auth

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// Params are the per-attempt values of one authorization-code flow.
type Params struct {
	// State binds the callback to the attempt (and to the browser, through a cookie).
	State string
	// Nonce is echoed inside the OpenID Connect ID token.
	Nonce string
	// Verifier is the PKCE code verifier; the challenge is derived from it.
	Verifier string
	// RedirectURL is the registered callback URL.
	RedirectURL string
}

// Provider is one way to sign in. Implementations return only the provider's
// stable, opaque user id: no email, name or avatar is ever requested.
type Provider interface {
	// ID is the short lower-case name used in URLs and stored with the account.
	ID() string
	// Name is the label shown on the sign-in button.
	Name() string
	// AuthURL is where the browser is sent to sign in.
	AuthURL(p Params) string
	// Exchange trades the callback code for the provider's stable user id.
	Exchange(ctx context.Context, code string, p Params) (userID string, err error)
}

// httpClient is used for every call to a provider.
func httpClient() *http.Client { return &http.Client{Timeout: 10 * time.Second} }

// ---- Google (OpenID Connect) -----------------------------------------------

// GoogleConfig configures the Google provider. Empty URLs use Google's own;
// tests point them at a fake server.
type GoogleConfig struct {
	ClientID, ClientSecret string
	Issuer                 string // default https://accounts.google.com
	AuthURL                string // default https://accounts.google.com/o/oauth2/v2/auth
	TokenURL               string // default https://oauth2.googleapis.com/token
	JWKSURL                string // default https://www.googleapis.com/oauth2/v3/certs
	Client                 *http.Client
	Now                    func() time.Time
}

type googleProvider struct {
	cfg      GoogleConfig
	client   *http.Client
	verifier *oidc.IDTokenVerifier
}

// NewGoogle builds the Google provider: authorization-code flow with PKCE,
// `state` and `nonce`, asking only for the `openid` scope, and a verified ID
// token (issuer, audience, expiry, signature from Google's published keys).
func NewGoogle(cfg GoogleConfig) Provider {
	def := func(v *string, d string) {
		if *v == "" {
			*v = d
		}
	}
	def(&cfg.Issuer, "https://accounts.google.com")
	def(&cfg.AuthURL, "https://accounts.google.com/o/oauth2/v2/auth")
	def(&cfg.TokenURL, "https://oauth2.googleapis.com/token")
	def(&cfg.JWKSURL, "https://www.googleapis.com/oauth2/v3/certs")
	client := cfg.Client
	if client == nil {
		client = httpClient()
	}
	// The key set is fetched lazily on the first verification and cached.
	keys := oidc.NewRemoteKeySet(oidc.ClientContext(context.Background(), client), cfg.JWKSURL)
	return &googleProvider{
		cfg: cfg, client: client,
		verifier: oidc.NewVerifier(cfg.Issuer, keys, &oidc.Config{
			ClientID: cfg.ClientID, SupportedSigningAlgs: []string{oidc.RS256}, Now: cfg.Now,
		}),
	}
}

func (g *googleProvider) ID() string   { return "google" }
func (g *googleProvider) Name() string { return "Google" }

func (g *googleProvider) oauth(redirect string) *oauth2.Config {
	return &oauth2.Config{
		ClientID: g.cfg.ClientID, ClientSecret: g.cfg.ClientSecret, RedirectURL: redirect,
		Endpoint: oauth2.Endpoint{AuthURL: g.cfg.AuthURL, TokenURL: g.cfg.TokenURL, AuthStyle: oauth2.AuthStyleInParams},
		Scopes:   []string{oidc.ScopeOpenID},
	}
}

func (g *googleProvider) AuthURL(p Params) string {
	return g.oauth(p.RedirectURL).AuthCodeURL(p.State,
		oauth2.S256ChallengeOption(p.Verifier),
		oauth2.SetAuthURLParam("nonce", p.Nonce),
		oauth2.SetAuthURLParam("prompt", "select_account"))
}

func (g *googleProvider) Exchange(ctx context.Context, code string, p Params) (string, error) {
	ctx = context.WithValue(ctx, oauth2.HTTPClient, g.client)
	tok, err := g.oauth(p.RedirectURL).Exchange(ctx, code, oauth2.VerifierOption(p.Verifier))
	if err != nil {
		return "", fmt.Errorf("google token exchange: %w", err)
	}
	raw, _ := tok.Extra("id_token").(string)
	if raw == "" {
		return "", errors.New("google: no id_token in token response")
	}
	idt, err := g.verifier.Verify(oidc.ClientContext(ctx, g.client), raw)
	if err != nil {
		return "", fmt.Errorf("google id token: %w", err)
	}
	if subtle.ConstantTimeCompare([]byte(idt.Nonce), []byte(p.Nonce)) != 1 || p.Nonce == "" {
		return "", errors.New("google: nonce mismatch")
	}
	if idt.Subject == "" {
		return "", errors.New("google: empty subject")
	}
	return idt.Subject, nil
}

// ---- GitHub (plain OAuth2) -------------------------------------------------

// GitHubConfig configures the GitHub provider; empty URLs use GitHub's own.
type GitHubConfig struct {
	ClientID, ClientSecret string
	AuthURL                string // default https://github.com/login/oauth/authorize
	TokenURL               string // default https://github.com/login/oauth/access_token
	APIURL                 string // default https://api.github.com
	Client                 *http.Client
}

type githubProvider struct {
	cfg    GitHubConfig
	client *http.Client
}

// NewGitHub builds the GitHub provider. No scope is requested, so the token
// only reads public data; the one call made is GET /user, of which only the
// numeric id is kept.
func NewGitHub(cfg GitHubConfig) Provider {
	def := func(v *string, d string) {
		if *v == "" {
			*v = d
		}
	}
	def(&cfg.AuthURL, "https://github.com/login/oauth/authorize")
	def(&cfg.TokenURL, "https://github.com/login/oauth/access_token")
	def(&cfg.APIURL, "https://api.github.com")
	cfg.APIURL = strings.TrimRight(cfg.APIURL, "/")
	client := cfg.Client
	if client == nil {
		client = httpClient()
	}
	return &githubProvider{cfg: cfg, client: client}
}

func (g *githubProvider) ID() string   { return "github" }
func (g *githubProvider) Name() string { return "GitHub" }

func (g *githubProvider) oauth(redirect string) *oauth2.Config {
	return &oauth2.Config{
		ClientID: g.cfg.ClientID, ClientSecret: g.cfg.ClientSecret, RedirectURL: redirect,
		Endpoint: oauth2.Endpoint{AuthURL: g.cfg.AuthURL, TokenURL: g.cfg.TokenURL, AuthStyle: oauth2.AuthStyleInParams},
	}
}

func (g *githubProvider) AuthURL(p Params) string {
	return g.oauth(p.RedirectURL).AuthCodeURL(p.State, oauth2.S256ChallengeOption(p.Verifier))
}

func (g *githubProvider) Exchange(ctx context.Context, code string, p Params) (string, error) {
	ctx = context.WithValue(ctx, oauth2.HTTPClient, g.client)
	tok, err := g.oauth(p.RedirectURL).Exchange(ctx, code, oauth2.VerifierOption(p.Verifier))
	if err != nil {
		return "", fmt.Errorf("github token exchange: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.cfg.APIURL+"/user", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := g.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("github user: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github user: status %d", resp.StatusCode)
	}
	var u struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&u); err != nil {
		return "", fmt.Errorf("github user: %w", err)
	}
	if u.ID <= 0 {
		return "", errors.New("github: empty user id")
	}
	return strconv.FormatInt(u.ID, 10), nil
}

// ---- Dev fake provider -----------------------------------------------------

var devUserRE = regexp.MustCompile(`^[a-z0-9_-]{1,32}$`)

// DevProvider is a stand-in provider for trying the whole flow on a developer
// machine without OAuth credentials: its "sign-in page" lets you type any user
// name. It is enabled only by AUTH_DEV_FAKE=true and only when the public URL is
// localhost (the server refuses to start otherwise); it also refuses requests
// whose Host is not local. It exists so the real login, cookie, claim and
// deletion paths can be exercised end to end; it proves nothing about Google or
// GitHub.
type DevProvider struct{ base string }

// NewDev builds the dev provider for the local base URL.
func NewDev(base string) *DevProvider { return &DevProvider{base: strings.TrimRight(base, "/")} }

func (d *DevProvider) ID() string   { return "dev" }
func (d *DevProvider) Name() string { return "Dev sign-in (local only)" }

func (d *DevProvider) AuthURL(p Params) string {
	return d.base + "/auth/dev/authorize?" + url.Values{"state": {p.State}}.Encode()
}

func (d *DevProvider) Exchange(_ context.Context, code string, _ Params) (string, error) {
	if !devUserRE.MatchString(code) {
		return "", errors.New("dev: user names are 1-32 lower-case letters, digits, - or _")
	}
	return code, nil
}

// authorize serves the fake provider's sign-in page.
func (d *DevProvider) authorize(w http.ResponseWriter, r *http.Request) {
	if !IsLocalHost(r.Host) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Dev sign-in</title>
<body style="font-family:system-ui;max-width:26rem;margin:3rem auto;padding:0 1rem">
<h1>Dev sign-in</h1>
<p>This is a fake identity provider for local development. Pick any user name; the same name always gives the same account.</p>
<form method="get" action="/auth/dev/callback">
<input type="hidden" name="state" value="%s">
<label>User name <input name="code" value="alice" pattern="[a-z0-9_-]{1,32}" required autofocus></label>
<button>Sign in</button>
</form></body>`, html.EscapeString(r.URL.Query().Get("state")))
}

// IsLocalHost reports whether a host (optionally with a port) is localhost,
// 127.0.0.1 or [::1].
func IsLocalHost(hostport string) bool {
	h := hostport
	if u, err := url.Parse("//" + hostport); err == nil && u.Hostname() != "" {
		h = u.Hostname()
	}
	switch strings.ToLower(h) {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}

// ---- configuration from the environment ------------------------------------

// ProvidersFromEnv builds the enabled providers. A provider is enabled when both
// its client id and secret are set; one without the other is an error. publicURL
// (the -public-url flag) is required as soon as any provider is enabled, since
// OAuth callbacks must be exact, registered URLs. AUTH_DEV_FAKE=true adds the
// dev provider and is refused unless publicURL is a localhost URL.
func ProvidersFromEnv(getenv func(string) string, publicURL string) ([]Provider, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	var out []Provider
	pair := func(prefix string) (id, secret string, on bool, err error) {
		id, secret = strings.TrimSpace(getenv(prefix+"_CLIENT_ID")), strings.TrimSpace(getenv(prefix+"_CLIENT_SECRET"))
		switch {
		case id == "" && secret == "":
			return "", "", false, nil
		case id == "" || secret == "":
			return "", "", false, fmt.Errorf("%s_CLIENT_ID and %s_CLIENT_SECRET must both be set", prefix, prefix)
		}
		return id, secret, true, nil
	}
	if id, secret, on, err := pair("AUTH_GOOGLE"); err != nil {
		return nil, err
	} else if on {
		out = append(out, NewGoogle(GoogleConfig{ClientID: id, ClientSecret: secret}))
	}
	if id, secret, on, err := pair("AUTH_GITHUB"); err != nil {
		return nil, err
	} else if on {
		out = append(out, NewGitHub(GitHubConfig{ClientID: id, ClientSecret: secret}))
	}
	if v := strings.ToLower(strings.TrimSpace(getenv("AUTH_DEV_FAKE"))); v == "true" || v == "1" || v == "yes" {
		u, err := ParseBaseURL(publicURL)
		if err != nil || !IsLocalHost(u.Host) {
			return nil, errors.New("AUTH_DEV_FAKE=true is only allowed when -public-url is a localhost URL (for example http://localhost:8000)")
		}
		out = append(out, NewDev(u.String()))
	}
	if len(out) > 0 {
		if _, err := ParseBaseURL(publicURL); err != nil {
			return nil, fmt.Errorf("sign-in needs -public-url (the site's base URL, used for OAuth callbacks): %w", err)
		}
	}
	return out, nil
}

// ParseBaseURL parses an absolute http(s) base URL without path, query or user info.
func ParseBaseURL(s string) (*url.URL, error) {
	s = strings.TrimRight(strings.TrimSpace(s), "/")
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, fmt.Errorf("%q is not an http(s) base URL like https://example.com", s)
	}
	u.Path = ""
	return u, nil
}
