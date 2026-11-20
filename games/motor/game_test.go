package motor

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	motorgraph "github.com/wricardo/drbrain/games/motor/graph"
	"github.com/wricardo/drbrain/graph"
	"github.com/wricardo/drbrain/platform"
	"github.com/wricardo/drbrain/platform/account"
	accountgraph "github.com/wricardo/drbrain/platform/account/graph"
	"github.com/wricardo/drbrain/platform/auth"
	"github.com/wricardo/drbrain/platform/config"
	"github.com/wricardo/drbrain/platform/httpserve"
)

func newAccounts(t *testing.T, dir string, password bool) (*account.Service, *auth.Auth, []auth.Provider) {
	t.Helper()
	store, err := account.Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	accounts := account.New(account.Options{Store: store, PasswordSignIn: password})
	providers, err := auth.Providers(auth.ProvidersConfig{}, "")
	if err != nil {
		t.Fatal(err)
	}
	authn, err := auth.New(auth.Config{Accounts: accounts, Password: password})
	if err != nil {
		t.Fatal(err)
	}
	return accounts, authn, providers
}

// TestCookiesReachUnifiedGraphQL proves the login cookie is scoped to the
// origin and reaches /graphql, where the global account namespace resolves it.
func TestCookiesReachUnifiedGraphQL(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MOTOR_MAPS_DIR", "maps")
	t.Setenv("MOTOR_USER_MAPS_DIR", dir+"/maps-user")
	t.Setenv("MOTOR_SESSIONS_DIR", dir+"/sessions")

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	accounts, authn, _ := newAccounts(t, dir+"/accounts", true)
	motorMux := http.NewServeMux()
	game := New()
	if err := game.Mount(motorMux, platform.Deps{Config: config.Config{}, Logger: logger, Accounts: accounts, Auth: authn}); err != nil {
		t.Fatalf("mount motor: %v", err)
	}
	t.Cleanup(func() { _ = game.Shutdown(context.Background()) })

	resolver, ok := game.GraphQLResolver().(*motorgraph.Resolver)
	if !ok {
		t.Fatalf("GraphQLResolver = %T, want *motorgraph.Resolver", game.GraphQLResolver())
	}
	unified := graph.NewHandler(graph.Options{
		Resolver:      graph.New(accountgraph.New(accounts, authn), nil, nil, resolver, nil),
		Introspection: true,
		Auth:          authn,
	})

	router := httpserve.NewRouter(httpserve.SPAHandler(t.TempDir()))
	router.SetLogger(nil)
	if err := router.Mount(httpserve.Mount{Game: "motor", Prefix: "/motor", API: game.API(), Handler: motorMux}); err != nil {
		t.Fatal(err)
	}
	if err := router.MountPlatform("/graphql", unified); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}

	body := `{"query":"mutation { account { registerWithPassword(handle:\"cookie_pilot\", password:\"tangerine-ladder-42\") { handle } } }"}`
	resp, err := client.Post(ts.URL+"/graphql", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("register status = %d: %s", resp.StatusCode, raw)
	}
	var sessionCookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == auth.SessionCookie {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatalf("no %s cookie in %v", auth.SessionCookie, resp.Cookies())
	}
	if sessionCookie.Path != "/" {
		t.Errorf("%s Path = %q, want /", auth.SessionCookie, sessionCookie.Path)
	}

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/graphql", strings.NewReader(`{"query":"{ account { me { handle } } }"}`))
	req.Header.Set("Content-Type", "application/json")
	resp2, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	me, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if !strings.Contains(string(me), "cookie_pilot") {
		t.Errorf("unified handler did not read the login cookie: %s", me)
	}
}

// TestDevSignInAtRoot follows the OAuth-shaped redirect flow at /auth: login ->
// provider authorize -> callback, and checks the cookie paths.
func TestDevSignInAtRoot(t *testing.T) {
	dir := t.TempDir()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	base := "http://" + ln.Addr().String()

	t.Setenv("MOTOR_MAPS_DIR", "maps")
	t.Setenv("MOTOR_USER_MAPS_DIR", dir+"/maps-user")
	t.Setenv("MOTOR_SESSIONS_DIR", dir+"/sessions")

	store, err := account.Open(dir+"/accounts", nil)
	if err != nil {
		t.Fatal(err)
	}
	accounts := account.New(account.Options{Store: store})
	providers, err := auth.Providers(auth.ProvidersConfig{DevFake: true}, base)
	if err != nil {
		t.Fatal(err)
	}
	authn, err := auth.New(auth.Config{Providers: providers, Accounts: accounts, PublicURL: base})
	if err != nil {
		t.Fatal(err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mux := http.NewServeMux()
	game := New()
	if err := game.Mount(mux, platform.Deps{Config: config.Config{PublicURL: base}, Logger: logger, Accounts: accounts, Auth: authn}); err != nil {
		t.Fatalf("mount motor: %v", err)
	}
	t.Cleanup(func() { _ = game.Shutdown(context.Background()) })

	router := httpserve.NewRouter(httpserve.SPAHandler(t.TempDir()))
	router.SetLogger(nil)
	if err := router.Mount(httpserve.Mount{Game: "motor", Prefix: "/motor", API: game.API(), Handler: mux}); err != nil {
		t.Fatal(err)
	}
	if err := router.MountPlatform("/auth", authn.Handler()); err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: router.Handler()}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	ts := base

	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	resp, err := client.Get(ts + "/auth/dev/login")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("login status = %d", resp.StatusCode)
	}
	authURL, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if authURL.Path != "/auth/dev/authorize" {
		t.Errorf("authorize path = %q", authURL.Path)
	}
	var flow *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "drbrain_oauth" {
			flow = c
		}
	}
	if flow == nil || flow.Path != "/auth" {
		t.Fatalf("flow cookie = %+v, want Path=/auth", flow)
	}

	resp, err = client.Get(ts + authURL.RequestURI())
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(page), `action="/auth/dev/callback"`) {
		t.Errorf("dev page does not post to the callback:\n%s", page)
	}

	callback := "/auth/dev/callback?state=" + url.QueryEscape(authURL.Query().Get("state")) + "&code=alice"
	resp, err = client.Get(ts + callback)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("callback status = %d", resp.StatusCode)
	}
	var session *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == auth.SessionCookie {
			session = c
		}
	}
	if session == nil || session.Path != "/" {
		t.Fatalf("session cookie = %+v, want %s Path=/", session, auth.SessionCookie)
	}
}
