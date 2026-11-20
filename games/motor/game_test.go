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

	"github.com/wricardo/drbrain/games/motor/auth"
	motorgraph "github.com/wricardo/drbrain/games/motor/graph"
	"github.com/wricardo/drbrain/graph"
	"github.com/wricardo/drbrain/platform"
	"github.com/wricardo/drbrain/platform/config"
	"github.com/wricardo/drbrain/platform/httpserve"
)

// TestCookiesReachUnifiedGraphQL proves the password sign-in cookie is scoped
// to the origin and therefore reaches /graphql, where the motor namespace
// resolves the viewer.
func TestCookiesReachUnifiedGraphQL(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MOTOR_MAPS_DIR", "maps")
	t.Setenv("MOTOR_USER_MAPS_DIR", dir+"/maps-user")
	t.Setenv("MOTOR_SESSIONS_DIR", dir+"/sessions")
	t.Setenv("MOTOR_ACCOUNTS_DIR", dir+"/accounts")
	t.Setenv("MOTOR_DATABASE_URL", "")
	t.Setenv("MOTOR_PUBLIC_URL", "")
	t.Setenv("DATABASE_URL", "")
	t.Setenv("AUTH_PASSWORD", "")
	t.Setenv("AUTH_DEV_FAKE", "false")

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	motorMux := http.NewServeMux()
	game := New()
	if err := game.Mount(motorMux, platform.Deps{Config: config.Config{}, Logger: logger}); err != nil {
		t.Fatalf("mount motor: %v", err)
	}
	t.Cleanup(func() { _ = game.Shutdown(context.Background()) })

	resolver, ok := game.GraphQLResolver().(*motorgraph.Resolver)
	if !ok {
		t.Fatalf("GraphQLResolver = %T, want *motorgraph.Resolver", game.GraphQLResolver())
	}
	authn, _ := game.GraphQLAuth().(*auth.Auth)
	unified := graph.NewHandler(graph.Options{
		Resolver:      graph.New(nil, nil, resolver, nil),
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

	body := `{"query":"mutation { motorProgramming { registerWithPassword(handle:\"cookie_pilot\", password:\"tangerine-ladder-42\") { handle } } }"}`
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

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/graphql", strings.NewReader(`{"query":"{ motorProgramming { me { handle } } }"}`))
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

// TestDevSignInUnderMotorPrefix follows the real OAuth-shaped redirect flow:
// login -> provider authorize -> callback, all under /motor/auth, and checks
// the cookies and callback URL carry the mount prefix.
func TestDevSignInUnderMotorPrefix(t *testing.T) {
	dir := t.TempDir()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	base := "http://" + ln.Addr().String()

	t.Setenv("MOTOR_MAPS_DIR", "maps")
	t.Setenv("MOTOR_USER_MAPS_DIR", dir+"/maps-user")
	t.Setenv("MOTOR_SESSIONS_DIR", dir+"/sessions")
	t.Setenv("MOTOR_ACCOUNTS_DIR", dir+"/accounts")
	t.Setenv("MOTOR_DATABASE_URL", "")
	t.Setenv("DATABASE_URL", "")
	t.Setenv("MOTOR_PUBLIC_URL", base)
	t.Setenv("AUTH_DEV_FAKE", "true")
	t.Setenv("AUTH_PASSWORD", "false")

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mux := http.NewServeMux()
	game := New()
	if err := game.Mount(mux, platform.Deps{Config: config.Config{PublicURL: base}, Logger: logger}); err != nil {
		t.Fatalf("mount motor: %v", err)
	}
	t.Cleanup(func() { _ = game.Shutdown(context.Background()) })

	router := httpserve.NewRouter(httpserve.SPAHandler(t.TempDir()))
	router.SetLogger(nil)
	if err := router.Mount(httpserve.Mount{Game: "motor", Prefix: "/motor", API: game.API(), Handler: mux}); err != nil {
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

	resp, err := client.Get(ts + "/motor/auth/dev/login")
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
	if authURL.Path != "/motor/auth/dev/authorize" {
		t.Errorf("authorize path = %q", authURL.Path)
	}
	var flow *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "motor_oauth" {
			flow = c
		}
	}
	if flow == nil || flow.Path != "/motor/auth" {
		t.Fatalf("flow cookie = %+v, want Path=/motor/auth", flow)
	}

	// The provider page posts back to the prefixed callback.
	resp, err = client.Get(ts + authURL.RequestURI())
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(page), `action="/motor/auth/dev/callback"`) {
		t.Errorf("dev page does not post to the prefixed callback:\n%s", page)
	}

	callback := "/motor/auth/dev/callback?state=" + url.QueryEscape(authURL.Query().Get("state")) + "&code=alice"
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
