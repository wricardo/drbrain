package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wricardo/drbrain-motor-programming/auth"
	"github.com/wricardo/drbrain-motor-programming/game/account"
	"github.com/wricardo/drbrain-motor-programming/game/config"
	"github.com/wricardo/drbrain-motor-programming/game/service"
	"github.com/wricardo/drbrain-motor-programming/game/session"
	"github.com/wricardo/drbrain-motor-programming/transport/websocket"
)

// newPasswordServer serves the real stack with username/password sign-in only
// (no OAuth provider, which is what a deployment that wants no third party does).
func newPasswordServer(t *testing.T) *httptest.Server {
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
	hub := websocket.NewHub()
	svc := service.New(maps, session.NewManager(nil, 0), hub, service.Options{Accounts: store, PasswordSignIn: true})
	t.Cleanup(svc.Shutdown)

	ts := httptest.NewUnstartedServer(nil)
	base := "http://" + ts.Listener.Addr().String()
	a, err := auth.New(auth.Config{Accounts: svc, PublicURL: base, Password: true, Limiter: auth.NewLimiter(10000, 1, nil)})
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewServer(svc, hub, Options{Auth: a, UIDirs: []string{filepath.Join(t.TempDir(), "missing")}})
	if err != nil {
		t.Fatal(err)
	}
	ts.Config.Handler = s
	ts.Start()
	t.Cleanup(ts.Close)
	return ts
}

// pwPost posts JSON to an /auth/password route as this browser.
func (b *browser) pwPost(path, handle, password string, override map[string]string) (*http.Response, map[string]any) {
	b.t.Helper()
	body, _ := json.Marshal(map[string]string{"handle": handle, "password": password})
	req, _ := http.NewRequest(http.MethodPost, b.base+path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", b.base)
	for k, v := range override {
		req.Header.Set(k, v)
	}
	resp, raw := b.do(req)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return resp, out
}

const (
	pwMeQ     = `{ me { handle hasPassword } authProviders { id name } }`
	pwChangeQ = `mutation($c: String!, $n: String!) { changePassword(currentPassword: $c, newPassword: $n) }`
)

func TestPasswordAccountsEndToEnd(t *testing.T) {
	ts := newPasswordServer(t)
	alice, laptop, evil := newBrowser(t, ts.URL), newBrowser(t, ts.URL), newBrowser(t, ts.URL)

	// The only way in is the password form.
	out := alice.gql(pwMeQ, nil)
	provs := dig(t, out, "data", "authProviders").([]any)
	if len(provs) != 1 || provs[0].(map[string]any)["id"] != "password" {
		t.Fatalf("authProviders = %v", provs)
	}

	// Register: the username becomes the handle, and the player is signed in.
	resp, reg := alice.pwPost("/auth/password/register", "Speedy_Fox", "tangerine-ladder-42", nil)
	if resp.StatusCode != http.StatusOK || reg["handle"] != "speedy_fox" {
		t.Fatalf("register = %d %v", resp.StatusCode, reg)
	}
	me := dig(t, alice.gql(pwMeQ, nil), "data", "me").(map[string]any)
	if me["handle"] != "speedy_fox" || me["hasPassword"] != true {
		t.Fatalf("me = %v", me)
	}
	if strings.Contains(string(mustJSON(alice.gql(`{ me { handle displayName profilePublic createdAt hasPassword } }`, nil))), "argon") {
		t.Fatal("a hash is exposed through the API")
	}

	// The handle is public: the profile resolves and the account plays like any other.
	id, _ := alice.play("typed name")
	board := evil.gql(boardQ, nil)
	if e := dig(t, board, "data", "leaderboard", "entries").([]any)[0].(map[string]any); e["handle"] != "speedy_fox" || e["sessionId"] != id {
		t.Fatalf("entry = %v", e)
	}

	// A second browser signs in with the password.
	if resp, _ := laptop.pwPost("/auth/password/login", "speedy_fox", "wrong-guess-here", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong password = %d", resp.StatusCode)
	}
	if resp, _ := laptop.pwPost("/auth/password/login", "Speedy_Fox", "tangerine-ladder-42", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("login = %d", resp.StatusCode)
	}
	if dig(t, laptop.gql(pwMeQ, nil), "data", "me", "handle") != "speedy_fox" {
		t.Fatal("the laptop is not signed in")
	}

	// Changing the password needs the current one, applies the rules, and signs the other browser out.
	for name, args := range map[string][2]string{
		"wrong current": {"nope-nope-nope", "brand-new-pass-77"},
		"weak new":      {"tangerine-ladder-42", "password123"},
	} {
		if code := firstErrCode(alice.gql(pwChangeQ, map[string]any{"c": args[0], "n": args[1]})); code != service.CodeInvalidArgument {
			t.Fatalf("%s: code %q", name, code)
		}
	}
	if out := alice.gql(pwChangeQ, map[string]any{"c": "tangerine-ladder-42", "n": "brand-new-pass-77"}); dig(t, out, "data", "changePassword") != true {
		t.Fatalf("changePassword: %v", out)
	}
	if dig(t, alice.gql(pwMeQ, nil), "data", "me") == nil {
		t.Fatal("the browser that changed the password was signed out")
	}
	if dig(t, laptop.gql(pwMeQ, nil), "data", "me") != nil {
		t.Fatal("the other browser stayed signed in after the password change")
	}

	// Old password dead, new password works.
	if resp, _ := laptop.pwPost("/auth/password/login", "speedy_fox", "tangerine-ladder-42", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old password = %d", resp.StatusCode)
	}
	if resp, _ := laptop.pwPost("/auth/password/login", "speedy_fox", "brand-new-pass-77", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("new password = %d", resp.StatusCode)
	}

	// Another site can neither change the password nor sign in through the browser.
	if code := firstErrCode(func() map[string]any {
		_, o := alice.gqlWith(pwChangeQ, map[string]any{"c": "brand-new-pass-77", "n": "attacker-pass-99"}, map[string]string{"Origin": "https://evil.example"})
		return o
	}()); code != service.CodeUnauthenticated {
		t.Fatalf("cross-site changePassword = %q", code)
	}
	if resp, _ := evil.pwPost("/auth/password/login", "speedy_fox", "brand-new-pass-77", map[string]string{"Origin": "https://evil.example"}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-site login = %d", resp.StatusCode)
	}

	// Logout, then deleting the account frees the username.
	alice.gql(`mutation { logout }`, nil)
	if dig(t, alice.gql(pwMeQ, nil), "data", "me") != nil {
		t.Fatal("still signed in after logout")
	}
	if resp, _ := alice.pwPost("/auth/password/login", "speedy_fox", "brand-new-pass-77", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("login after logout = %d", resp.StatusCode)
	}
	if out := alice.gql(`mutation { deleteAccount(confirmHandle: "speedy_fox") }`, nil); out["errors"] != nil {
		t.Fatalf("deleteAccount: %v", out)
	}
	if resp, _ := alice.pwPost("/auth/password/login", "speedy_fox", "brand-new-pass-77", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("login after delete = %d", resp.StatusCode)
	}
}

func TestPasswordSignInAbsentUnlessEnabled(t *testing.T) {
	ts := newRealServer(t) // no auth at all
	b := newBrowser(t, ts.URL)
	for _, p := range []string{"/auth/password/register", "/auth/password/login"} {
		if resp, _ := b.pwPost(p, "speedy_fox", "tangerine-ladder-42", nil); resp.StatusCode != http.StatusNotFound {
			t.Errorf("POST %s = %d, want 404", p, resp.StatusCode)
		}
	}
	// A dev-provider deployment without AUTH_PASSWORD has no password routes either.
	dev := newAccountsServer(t, "")
	bd := newBrowser(t, dev.URL)
	if resp, _ := bd.pwPost("/auth/password/register", "speedy_fox", "tangerine-ladder-42", nil); resp.StatusCode == http.StatusOK {
		t.Fatal("registration works with AUTH_PASSWORD off")
	}
	if dig(t, bd.gql(`{ me { hasPassword } }`, nil), "data", "me") != nil {
		t.Fatal("a viewer appeared")
	}
	// OAuth/dev accounts report no password and cannot set one.
	bd.signIn("alice", "/")
	if dig(t, bd.gql(`{ me { hasPassword } }`, nil), "data", "me", "hasPassword") != false {
		t.Fatal("a dev account reports a password")
	}
	if code := firstErrCode(bd.gql(pwChangeQ, map[string]any{"c": "x", "n": "tangerine-ladder-42"})); code == "" {
		t.Fatal("changePassword on an account without a password succeeded")
	}
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
