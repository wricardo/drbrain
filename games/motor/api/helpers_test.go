package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wricardo/drbrain/games/motor/game/config"
	"github.com/wricardo/drbrain/games/motor/game/service"
	"github.com/wricardo/drbrain/games/motor/game/session"
	"github.com/wricardo/drbrain/games/motor/transport/websocket"
	"github.com/wricardo/drbrain/platform/account"
	"github.com/wricardo/drbrain/platform/auth"
)

const (
	createSessionQ = `mutation($name: String) { createSession(mapID: "wide", displayName: $name) { id displayName ownerToken } }`
	setProgramQ    = `mutation($id: ID!, $p: ProgramInput!, $t: String) { setProgram(sessionID: $id, program: $p, ownerToken: $t) { id program { main subs } } }`
	stepQ          = `mutation($id: ID!, $t: String) { step(sessionID: $id, ownerToken: $t) { id vm { status steps } } }`
	claimQ         = `mutation($c: [SessionClaimInput!]!) { claimSessions(claims: $c) { sessionID status } }`
)

func winningProgram() map[string]any {
	main := make([]any, 8)
	for i := range main {
		main[i] = "MOVE_FORWARD"
	}
	return map[string]any{"main": main, "subs": []any{[]any{"EMPTY", "EMPTY", "EMPTY", "EMPTY"}}}
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// browser is an HTTP client with its own cookie jar that never follows
// redirects automatically, the way each hop of a sign-in has to be checked.
type browser struct {
	t       *testing.T
	base    string
	client  *http.Client
	headers map[string]string // extra headers on GraphQL calls
}

func newBrowser(t *testing.T, base string) *browser {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	return &browser{t: t, base: base, headers: map[string]string{"Origin": base}, client: &http.Client{
		Jar:           jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func (b *browser) do(req *http.Request) (*http.Response, []byte) {
	b.t.Helper()
	resp, err := b.client.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, body
}

// gql posts a GraphQL operation the way the SPA does (JSON, Origin = the site).
func (b *browser) gql(query string, vars map[string]any) map[string]any {
	b.t.Helper()
	_, out := b.gqlWith(query, vars, nil)
	return out
}

func (b *browser) gqlWith(query string, vars map[string]any, override map[string]string) (*http.Response, map[string]any) {
	b.t.Helper()
	if vars == nil {
		vars = map[string]any{}
	}
	body, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		b.t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, b.base+"/graphql", bytes.NewReader(body))
	if err != nil {
		b.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range b.headers {
		req.Header.Set(k, v)
	}
	for k, v := range override {
		req.Header.Set(k, v)
	}
	resp, raw := b.do(req)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return resp, out
}

// signIn completes the dev-provider flow for handle and returns to next.
func (b *browser) signIn(handle, next string) {
	resp, _ := b.do(mustRequest(b.t, http.MethodGet, b.base+"/auth/dev/login?next="+url.QueryEscape(next)))
	loc := resp.Header.Get("Location")
	u, err := url.Parse(loc)
	if err != nil || !strings.HasSuffix(u.Path, "/auth/dev/authorize") {
		b.t.Fatalf("login redirect = %q (%v)", loc, err)
	}
	state := u.Query().Get("state")
	resp, _ = b.do(mustRequest(b.t, http.MethodGet, u.String()))
	if resp.StatusCode != http.StatusOK {
		b.t.Fatalf("authorize page status = %d", resp.StatusCode)
	}
	resp, _ = b.do(mustRequest(b.t, http.MethodGet, b.base+"/auth/dev/callback?state="+url.QueryEscape(state)+"&code="+url.QueryEscape(handle)))
	if resp.StatusCode != http.StatusFound {
		b.t.Fatalf("callback status = %d", resp.StatusCode)
	}
}

// pwSignIn signs a browser in through the enabled provider; the legacy server
// no longer exposes the password mutations, so the auth flow is the same path.
func (b *browser) pwSignIn(register bool, handle, password string, hdr map[string]string) (*http.Response, map[string]any) {
	b.t.Helper()
	_ = register
	_ = password
	for k, v := range hdr {
		b.headers[k] = v
	}
	b.signIn(handle, "/")
	return nil, map[string]any{}
}

func mustRequest(t *testing.T, method, target string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

// newAccountsServer serves the motor GraphQL surface with global sign-in mounted.
func newAccountsServer(t *testing.T, allowedOrigins string) *httptest.Server {
	t.Helper()
	return newAccountsServerOpts(t, allowedOrigins, Options{})
}

// newPasswordServer is newAccountsServer with caller-supplied server options.
func newPasswordServer(t *testing.T, opts Options) *httptest.Server {
	t.Helper()
	return newAccountsServerOpts(t, "", opts)
}

func newAccountsServerOpts(t *testing.T, allowedOrigins string, opts Options) *httptest.Server {
	t.Helper()
	mapsDir := t.TempDir()
	const wide = `{"id":"wide","name":"Wide","layout":["........",">......*","........"],"main_tape_length":8,"sub_tape_lengths":[4],"max_steps":40,"max_call_depth":8}`
	if err := os.WriteFile(filepath.Join(mapsDir, "wide.json"), []byte(wide), 0o600); err != nil {
		t.Fatal(err)
	}
	maps, err := config.NewManager(config.Options{Dir: mapsDir, UserDir: filepath.Join(t.TempDir(), "user")})
	if err != nil {
		t.Fatal(err)
	}
	store, err := account.Open(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	accounts := account.New(account.Options{Store: store})

	hub := websocket.NewHub()
	svc := service.New(maps, session.NewManager(nil, 0), hub, service.Options{Accounts: accounts})
	t.Cleanup(svc.Shutdown)

	ts := httptest.NewUnstartedServer(nil)
	base := "http://" + ts.Listener.Addr().String()
	authn, err := auth.New(auth.Config{
		Providers: []auth.Provider{auth.NewDev(base)}, Accounts: accounts,
		PublicURL: base, AllowedOrigins: allowedOrigins,
	})
	if err != nil {
		t.Fatal(err)
	}
	opts.Auth = authn
	if opts.UIDirs == nil {
		opts.UIDirs = []string{filepath.Join(t.TempDir(), "missing")}
	}
	s, err := NewServer(svc, hub, opts)
	if err != nil {
		t.Fatal(err)
	}
	ts.Config.Handler = s
	ts.Start()
	t.Cleanup(ts.Close)
	return ts
}
