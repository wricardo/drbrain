package motor

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wricardo/drbrain/games/motor/game/engine"
	motorgraph "github.com/wricardo/drbrain/games/motor/graph"
	"github.com/wricardo/drbrain/graph"
	"github.com/wricardo/drbrain/platform"
	"github.com/wricardo/drbrain/platform/account"
	accountgraph "github.com/wricardo/drbrain/platform/account/graph"
	"github.com/wricardo/drbrain/platform/auth"
	"github.com/wricardo/drbrain/platform/config"
	"github.com/wricardo/drbrain/platform/httpserve"
	"github.com/wricardo/drbrain/platform/pgdb/pgtest"
)

func newAccounts(t *testing.T, pool *pgxpool.Pool, password bool) (*account.Service, *auth.Auth, []auth.Provider) {
	t.Helper()
	accounts := account.New(account.Options{Store: account.NewPostgresStore(pool), PasswordSignIn: password})
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
	pool := pgtest.Pool(t)
	t.Setenv("MOTOR_MAPS_DIR", "maps")

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	accounts, authn, _ := newAccounts(t, pool, true)
	motorMux := http.NewServeMux()
	game := New()
	if err := game.Mount(motorMux, platform.Deps{Config: config.Config{}, Logger: logger, DB: pool, Accounts: accounts, Auth: authn}); err != nil {
		t.Fatalf("mount motor: %v", err)
	}
	t.Cleanup(func() { _ = game.Shutdown(context.Background()) })

	resolver, ok := game.GraphQLResolver().(*motorgraph.Resolver)
	if !ok {
		t.Fatalf("GraphQLResolver = %T, want *motorgraph.Resolver", game.GraphQLResolver())
	}
	unified := graph.NewHandler(graph.Options{
		Resolver:      graph.New(accountgraph.New(accounts, authn), nil, nil, resolver, nil, nil),
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

	body := `{"query":"mutation { account { registerWithPassword(handle:\"bearer_pilot\", password:\"tangerine-ladder-42\") { token user { handle } } } }"}`
	resp, err := client.Post(ts.URL+"/graphql", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(raw), `"token"`) {
		t.Fatalf("register status = %d: %s", resp.StatusCode, raw)
	}
	var out struct {
		Data struct {
			Account struct {
				RegisterWithPassword struct {
					Token string `json:"token"`
				} `json:"registerWithPassword"`
			} `json:"account"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.Data.Account.RegisterWithPassword.Token == "" {
		t.Fatalf("no token in %s (%v)", raw, err)
	}
	token := out.Data.Account.RegisterWithPassword.Token

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/graphql", strings.NewReader(`{"query":"{ account { me { handle } } }"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp2, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	me, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if !strings.Contains(string(me), "bearer_pilot") {
		t.Errorf("unified handler did not read the bearer token: %s", me)
	}

	// Without the header the same browser is anonymous: no cookie fallback.
	anon, _ := client.Post(ts.URL+"/graphql", "application/json", strings.NewReader(`{"query":"{ account { me { handle } } }"}`))
	body2, _ := io.ReadAll(anon.Body)
	anon.Body.Close()
	if !strings.Contains(string(body2), `"me":null`) {
		t.Errorf("me without a bearer token = %s", body2)
	}
}

// TestDevSignInAtRoot follows the OAuth-shaped redirect flow at /auth: login ->
// provider authorize -> callback, and checks the cookie paths.
func TestDevSignInAtRoot(t *testing.T) {
	pool := pgtest.Pool(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	base := "http://" + ln.Addr().String()

	t.Setenv("MOTOR_MAPS_DIR", "maps")

	accounts := account.New(account.Options{Store: account.NewPostgresStore(pool)})
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
	if err := game.Mount(mux, platform.Deps{Config: config.Config{PublicURL: base}, Logger: logger, DB: pool, Accounts: accounts, Auth: authn}); err != nil {
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
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil || loc.Query().Get("signin_code") == "" {
		t.Fatalf("callback did not hand off a code: %q", resp.Header.Get("Location"))
	}
	code := loc.Query().Get("signin_code")
	body, _ := json.Marshal(map[string]string{"code": code})
	tokResp, err := client.Post(ts+"/auth/token", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	defer tokResp.Body.Close()
	var got struct {
		Token string `json:"token"`
	}
	if tokResp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(tokResp.Body)
		t.Fatalf("token exchange = %d: %s", tokResp.StatusCode, raw)
	}
	_ = json.NewDecoder(tokResp.Body).Decode(&got)
	if got.Token == "" {
		t.Fatal("token exchange returned no token")
	}
}

// Motor Mount must add its account-deletion hooks, not replace the ones earlier
// games registered: a hook present before Mount still runs, and Motor's own map
// hook removes the account's drafts.
func TestAccountDeletionRunsMotorMapHooksWithoutReplacingOthers(t *testing.T) {
	pool := pgtest.Pool(t)
	t.Setenv("MOTOR_MAPS_DIR", "maps")

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	accounts, authn, _ := newAccounts(t, pool, true)
	earlier := 0
	accounts.AddOnDelete(func(string) error {
		earlier++
		return nil
	})

	game := New()
	if err := game.Mount(http.NewServeMux(), platform.Deps{
		Config: config.Config{}, Logger: logger, DB: pool, Accounts: accounts, Auth: authn,
	}); err != nil {
		t.Fatalf("mount motor: %v", err)
	}
	t.Cleanup(func() { _ = game.Shutdown(context.Background()) })

	acct, _, err := accounts.SignIn("google", "hook-user")
	if err != nil {
		t.Fatal(err)
	}
	mapCfg := engine.MapConfig{
		ID: "hook_map", Name: "Hook",
		Layout: []string{"....", ".>.*", "...."}, MainTapeLength: 4, SubTapeLengths: []int{2},
	}
	if _, err := game.svc.CreateMap(mapCfg, acct.ID); err != nil {
		t.Fatalf("create draft: %v", err)
	}
	if err := accounts.DeleteAccount(acct.ID); err != nil {
		t.Fatalf("delete account: %v", err)
	}
	if earlier != 1 {
		t.Fatalf("hook registered before Mount ran %d times, want 1 (Mount replaced it)", earlier)
	}
	if _, err := game.svc.MapEntry("hook_map"); err == nil {
		t.Fatal("Motor's map hook did not delete the account's draft")
	}
	if _, ok := accounts.Lookup(acct.ID); ok {
		t.Fatal("account row survived deletion")
	}
}
