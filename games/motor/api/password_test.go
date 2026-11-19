package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gorillaws "github.com/gorilla/websocket"
	"github.com/wricardo/drbrain/games/motor/auth"
	"github.com/wricardo/drbrain/games/motor/game/account"
	"github.com/wricardo/drbrain/games/motor/game/config"
	"github.com/wricardo/drbrain/games/motor/game/service"
	"github.com/wricardo/drbrain/games/motor/game/session"
	"github.com/wricardo/drbrain/games/motor/transport/websocket"
)

// newPasswordServer serves the real stack with username/password sign-in only
// (no OAuth provider, which is what a deployment that wants no third party does).
func newPasswordServer(t *testing.T, opts Options) *httptest.Server {
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
	opts.Auth = a
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

// setCookieHeader returns the Set-Cookie header for name, or "".
func setCookieHeader(resp *http.Response, name string) string {
	for _, v := range resp.Header.Values("Set-Cookie") {
		if strings.HasPrefix(v, name+"=") {
			return v
		}
	}
	return ""
}

// pwSignIn registers (register=true) or signs in through the GraphQL mutations
// as this browser, and returns the response and the decoded body.
func (b *browser) pwSignIn(register bool, handle, password string, override map[string]string) (*http.Response, map[string]any) {
	b.t.Helper()
	field := "logInWithPassword"
	if register {
		field = "registerWithPassword"
	}
	q := `mutation($h: String!, $p: String!) { ` + field + `(handle: $h, password: $p) { handle hasPassword } }`
	return b.gqlWith(q, map[string]any{"h": handle, "p": password}, override)
}

const (
	pwMeQ     = `{ me { handle hasPassword } authProviders { id name } }`
	pwChangeQ = `mutation($c: String!, $n: String!) { changePassword(currentPassword: $c, newPassword: $n) }`
)

func TestPasswordAccountsEndToEnd(t *testing.T) {
	ts := newPasswordServer(t, Options{})
	alice, laptop, evil := newBrowser(t, ts.URL), newBrowser(t, ts.URL), newBrowser(t, ts.URL)

	// The only way in is the password form.
	out := alice.gql(pwMeQ, nil)
	provs := dig(t, out, "data", "authProviders").([]any)
	if len(provs) != 1 || provs[0].(map[string]any)["id"] != "password" {
		t.Fatalf("authProviders = %v", provs)
	}

	// Register: the username becomes the handle, and the player is signed in.
	resp, reg := alice.pwSignIn(true, "Speedy_Fox", "tangerine-ladder-42", nil)
	if resp.StatusCode != http.StatusOK || dig(t, reg, "data", "registerWithPassword", "handle") != "speedy_fox" {
		t.Fatalf("register = %d %v", resp.StatusCode, reg)
	}
	// The cookie came back on the mutation's own response.
	if setCookieHeader(resp, "dbmp_session") == "" {
		t.Fatal("registering did not set the login cookie")
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
	if _, out := laptop.pwSignIn(false, "speedy_fox", "wrong-guess-here", nil); firstErrCode(out) != service.CodeUnauthenticated {
		t.Fatalf("wrong password = %v", out)
	}
	if _, out := laptop.pwSignIn(false, "Speedy_Fox", "tangerine-ladder-42", nil); firstErrCode(out) != "" {
		t.Fatalf("login = %v", out)
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
	if _, out := laptop.pwSignIn(false, "speedy_fox", "tangerine-ladder-42", nil); firstErrCode(out) != service.CodeUnauthenticated {
		t.Fatalf("old password = %v", out)
	}
	if _, out := laptop.pwSignIn(false, "speedy_fox", "brand-new-pass-77", nil); firstErrCode(out) != "" {
		t.Fatalf("new password = %v", out)
	}

	// Another site can neither change the password nor sign in through the browser.
	if code := firstErrCode(func() map[string]any {
		_, o := alice.gqlWith(pwChangeQ, map[string]any{"c": "brand-new-pass-77", "n": "attacker-pass-99"}, map[string]string{"Origin": "https://evil.example"})
		return o
	}()); code != service.CodeUnauthenticated {
		t.Fatalf("cross-site changePassword = %q", code)
	}
	if _, out := evil.pwSignIn(false, "speedy_fox", "brand-new-pass-77", map[string]string{"Origin": "https://evil.example"}); firstErrCode(out) != service.CodeForbidden {
		t.Fatalf("cross-site login = %v", out)
	}
	// ...and it did not sign the victim's browser in either.
	if dig(t, evil.gql(pwMeQ, nil), "data", "me") != nil {
		t.Fatal("a cross-site request signed a browser in")
	}

	// Logout, then deleting the account frees the username.
	alice.gql(`mutation { logout }`, nil)
	if dig(t, alice.gql(pwMeQ, nil), "data", "me") != nil {
		t.Fatal("still signed in after logout")
	}
	if _, out := alice.pwSignIn(false, "speedy_fox", "brand-new-pass-77", nil); firstErrCode(out) != "" {
		t.Fatalf("login after logout = %v", out)
	}
	if out := alice.gql(`mutation { deleteAccount(confirmHandle: "speedy_fox") }`, nil); out["errors"] != nil {
		t.Fatalf("deleteAccount: %v", out)
	}
	if _, out := alice.pwSignIn(false, "speedy_fox", "brand-new-pass-77", nil); firstErrCode(out) != service.CodeUnauthenticated {
		t.Fatalf("login after delete = %v", out)
	}
}

// The two mutations are the only way in, and a websocket connection has no HTTP
// response to set a cookie on: signing in over a subscription is refused rather
// than handing out a login the browser could never send back.
func TestPasswordSignInRefusedOverWebsocket(t *testing.T) {
	ts := newPasswordServer(t, Options{})
	d := gorillaws.Dialer{Subprotocols: []string{"graphql-transport-ws"}, HandshakeTimeout: 5 * time.Second}
	c, _, err := d.Dial("ws"+strings.TrimPrefix(ts.URL, "http")+"/graphql", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.WriteMessage(gorillaws.TextMessage, []byte(`{"type":"connection_init"}`)); err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, msg, err := c.ReadMessage(); err != nil || !strings.Contains(string(msg), "connection_ack") {
		t.Fatalf("ack = %q, %v", msg, err)
	}
	op := `{"id":"1","type":"subscribe","payload":{"query":"mutation { registerWithPassword(handle: \"speedy_fox\", password: \"tangerine-ladder-42\") { handle } }"}}`
	if err := c.WriteMessage(gorillaws.TextMessage, []byte(op)); err != nil {
		t.Fatal(err)
	}
	_, msg, err := c.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	var frame struct {
		Type    string `json:"type"`
		Payload struct {
			Errors []struct {
				Extensions struct {
					Code string `json:"code"`
				} `json:"extensions"`
			} `json:"errors"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(msg, &frame); err != nil {
		t.Fatalf("frame %q: %v", msg, err)
	}
	if frame.Type != "next" || len(frame.Payload.Errors) == 0 || frame.Payload.Errors[0].Extensions.Code != service.CodeForbidden {
		t.Fatalf("sign-in over a websocket = %s (%q)", msg, frame.Type)
	}
	// Nothing was created, and the browser is not signed in.
	b := newBrowser(t, ts.URL)
	if dig(t, b.gql(pwMeQ, nil), "data", "me") != nil {
		t.Fatal("a viewer appeared")
	}
	if _, out := b.pwSignIn(false, "speedy_fox", "tangerine-ladder-42", nil); firstErrCode(out) != service.CodeUnauthenticated {
		t.Fatalf("the account exists after a refused registration: %v", out)
	}
}

func TestPasswordSignInAbsentUnlessEnabled(t *testing.T) {
	ts := newRealServer(t) // no auth at all
	b := newBrowser(t, ts.URL)
	for _, register := range []bool{true, false} {
		if _, out := b.pwSignIn(register, "speedy_fox", "tangerine-ladder-42", nil); firstErrCode(out) != service.CodeForbidden {
			t.Errorf("register=%v with sign-in off = %v", register, out)
		}
	}
	// A dev-provider deployment without AUTH_PASSWORD has no password sign-in either.
	dev := newAccountsServer(t, "")
	bd := newBrowser(t, dev.URL)
	if _, out := bd.pwSignIn(true, "speedy_fox", "tangerine-ladder-42", nil); firstErrCode(out) != service.CodeForbidden {
		t.Fatalf("registration works with AUTH_PASSWORD off: %v", out)
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
