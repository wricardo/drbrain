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

	"github.com/wricardo/drbrain/games/motor/auth"
	"github.com/wricardo/drbrain/games/motor/game/account"
	"github.com/wricardo/drbrain/games/motor/game/config"
	"github.com/wricardo/drbrain/games/motor/game/service"
	"github.com/wricardo/drbrain/games/motor/game/session"
	"github.com/wricardo/drbrain/games/motor/transport/websocket"
)

// browser is an HTTP client with its own cookie jar that never follows
// redirects automatically, the way each hop of a sign-in has to be checked.
type browser struct {
	t       *testing.T
	base    string
	client  *http.Client
	headers map[string]string // extra headers on GraphQL calls
}

func newBrowser(t *testing.T, base string) *browser {
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
	body, _ := json.Marshal(map[string]any{"query": query, "variables": vars})
	req, _ := http.NewRequest(http.MethodPost, b.base+"/graphql", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range b.headers {
		req.Header.Set(k, v)
	}
	for k, v := range override {
		if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}
	resp, raw := b.do(req)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return resp, out
}

func (b *browser) get(path string) (*http.Response, []byte) {
	b.t.Helper()
	if !strings.HasPrefix(path, "http") {
		path = b.base + path
	}
	req, _ := http.NewRequest(http.MethodGet, path, nil)
	return b.do(req)
}

// signIn performs the whole dev-provider flow and returns the final redirect target.
func (b *browser) signIn(user, next string) string {
	b.t.Helper()
	resp, _ := b.get("/auth/dev/login?next=" + url.QueryEscape(next))
	if resp.StatusCode != http.StatusFound {
		b.t.Fatalf("login = %d", resp.StatusCode)
	}
	authz := resp.Header.Get("Location")
	if resp, _ = b.get(authz); resp.StatusCode != http.StatusOK {
		b.t.Fatalf("authorize page = %d", resp.StatusCode)
	}
	u, _ := url.Parse(authz)
	resp, _ = b.get("/auth/dev/callback?code=" + user + "&state=" + url.QueryEscape(u.Query().Get("state")))
	if resp.StatusCode != http.StatusFound {
		b.t.Fatalf("callback = %d", resp.StatusCode)
	}
	return resp.Header.Get("Location")
}

type acctServer struct {
	*httptest.Server
	svc service.GameService
	dir string
}

// newAccountsServer serves the real stack (file-backed accounts, the dev
// provider) on a real listener whose URL is the public URL.
func newAccountsServer(t *testing.T, allowedOrigins string) *acctServer {
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
	dir := t.TempDir()
	store, err := account.Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	hub := websocket.NewHub()
	svc := service.New(maps, session.NewManager(nil, 0), hub, service.Options{Accounts: store})
	t.Cleanup(svc.Shutdown)

	ts := httptest.NewUnstartedServer(nil)
	base := "http://" + ts.Listener.Addr().String()
	a, err := auth.New(auth.Config{
		Providers: []auth.Provider{auth.NewDev(base)}, Accounts: svc, PublicURL: base, AllowedOrigins: allowedOrigins,
		Limiter: auth.NewLimiter(10000, 1, nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewServer(svc, hub, Options{Auth: a, AllowedOrigins: allowedOrigins, UIDirs: []string{filepath.Join(t.TempDir(), "missing")}})
	if err != nil {
		t.Fatal(err)
	}
	ts.Config.Handler = s
	ts.Start()
	t.Cleanup(ts.Close)
	return &acctServer{Server: ts, svc: svc, dir: dir}
}

const (
	createSessionQ = `mutation($name: String) { createSession(mapID: "wide", displayName: $name) { id ownerToken } }`
	stepQ          = `mutation($id: ID!, $t: String) { step(sessionID: $id, ownerToken: $t) { vm { status } } }`
	setProgramQ    = `mutation($id: ID!, $p: ProgramInput!, $t: String) { setProgram(sessionID: $id, program: $p, ownerToken: $t) { id } }`
	meQ            = `{ me { handle displayName profilePublic createdAt handleChangeableAt } }`
	boardQ         = `query($sid: ID) { leaderboard(mapId: "wide", sessionId: $sid) { total entries { rank sessionId displayName handle mine slots steps } yours { sessionId handle mine } } }`
	profileGQL     = `query($h: String!) { profile(handle: $h) { handle displayName isMine runs { mapId total entry { rank sessionId slots steps } } } }`
	claimQ         = `mutation($c: [SessionClaimInput!]!) { claimSessions(claims: $c) { sessionID status } }`
)

// winningProgram is the 7-slot, 7-step direct route on the wide map.
func winningProgram() map[string]any {
	main := make([]string, 8)
	for i := range main {
		main[i] = "MOVE_FORWARD"
	}
	main[7] = "EMPTY"
	return map[string]any{"main": main, "subs": [][]string{{"EMPTY", "EMPTY", "EMPTY", "EMPTY"}}}
}

// play creates a session as b, wins it, and returns its id and owner token.
func (b *browser) play(name string) (id, token string) {
	b.t.Helper()
	out := b.gql(createSessionQ, map[string]any{"name": name})
	id, _ = dig(b.t, out, "data", "createSession", "id").(string)
	token, _ = dig(b.t, out, "data", "createSession", "ownerToken").(string)
	if id == "" || token == "" {
		b.t.Fatalf("createSession: %v", out)
	}
	if out = b.gql(setProgramQ, map[string]any{"id": id, "p": winningProgram(), "t": token}); out["errors"] != nil {
		b.t.Fatalf("setProgram: %v", out)
	}
	for range 20 {
		out = b.gql(stepQ, map[string]any{"id": id, "t": token})
		if dig(b.t, out, "data", "step", "vm", "status") == "WON" {
			return id, token
		}
	}
	b.t.Fatal("never won")
	return
}

func TestAccountsEndToEnd(t *testing.T) {
	ts := newAccountsServer(t, "")
	alice := newBrowser(t, ts.URL)
	anon := newBrowser(t, ts.URL)

	// Before signing in: no viewer, one provider on offer.
	if me := dig(t, alice.gql(meQ, nil), "data", "me"); me != nil {
		t.Fatalf("me before sign-in = %v", me)
	}
	provs := dig(t, alice.gql(`{ authProviders { id name } }`, nil), "data", "authProviders").([]any)
	if len(provs) != 1 || provs[0].(map[string]any)["id"] != "dev" {
		t.Fatalf("authProviders = %v", provs)
	}

	// Sign in: the final redirect is the validated next path; the cookie is hardened.
	if to := alice.signIn("alice", "/play/abc"); to != "/play/abc" {
		t.Fatalf("signed in, redirected to %q", to)
	}
	for _, c := range alice.client.Jar.Cookies(mustURL(ts.URL)) {
		if c.Name == auth.SessionCookie && len(c.Value) < 40 {
			t.Fatalf("session token too short: %d", len(c.Value))
		}
	}
	me := dig(t, alice.gql(meQ, nil), "data", "me").(map[string]any)
	handle, _ := me["handle"].(string)
	if service.ValidateHandle(handle) != nil || me["displayName"] != handle || me["profilePublic"] != true || me["handleChangeableAt"] != nil {
		t.Fatalf("me = %v", me)
	}
	if _, has := me["email"]; has {
		t.Fatal("me exposes an email")
	}

	// Play and win while signed in: the session belongs to the account, and the
	// owner token is shown only in the createSession response.
	id, token := alice.play("typed name")
	out := alice.gql(`query($id: ID!) { session(id: $id) { id ownerToken } sessions { ownerToken } }`, map[string]any{"id": id})
	if dig(t, out, "data", "session", "ownerToken") != nil {
		t.Fatalf("session query leaks the owner token: %v", out)
	}
	for _, s := range dig(t, out, "data", "sessions").([]any) {
		if s.(map[string]any)["ownerToken"] != nil {
			t.Fatalf("sessions list leaks an owner token: %v", out)
		}
	}
	if raw := ts.dumpSessions(t); strings.Contains(raw, token) {
		t.Fatal("the raw owner token is persisted")
	}

	// The board shows the handle (not the typed session name) and flags it as hers.
	board := alice.gql(boardQ, nil)
	first := dig(t, board, "data", "leaderboard", "entries").([]any)[0].(map[string]any)
	if first["handle"] != handle || first["displayName"] != handle || first["mine"] != true || first["sessionId"] != id {
		t.Fatalf("entry = %v", first)
	}
	if y := dig(t, board, "data", "leaderboard", "yours").(map[string]any); y["handle"] != handle {
		t.Fatalf("yours = %v", y)
	}
	// An anonymous browser sees the same entry, not flagged.
	anonBoard := anon.gql(boardQ, nil)
	if e := dig(t, anonBoard, "data", "leaderboard", "entries").([]any)[0].(map[string]any); e["handle"] != handle || e["mine"] != false {
		t.Fatalf("anonymous view = %v", e)
	}

	// Public profile: visible to everyone, with the best run per map.
	prof := dig(t, anon.gql(profileGQL, map[string]any{"h": strings.ToUpper(handle)}), "data", "profile").(map[string]any)
	runs := prof["runs"].([]any)
	if prof["handle"] != handle || prof["isMine"] != false || len(runs) != 1 || runs[0].(map[string]any)["mapId"] != "wide" {
		t.Fatalf("profile = %v", prof)
	}
	if e := runs[0].(map[string]any)["entry"].(map[string]any); e["sessionId"] != id || e["rank"] != float64(1) || e["slots"] != float64(7) {
		t.Fatalf("profile run = %v", e)
	}
	if dig(t, alice.gql(profileGQL, map[string]any{"h": handle}), "data", "profile", "isMine") != true {
		t.Fatal("owner not recognised on her profile")
	}

	// Private: hidden from everyone but her, and the board goes generic.
	out = alice.gql(`mutation { updateAccount(input: {profilePublic: false, displayName: "Real Name"}) { profilePublic displayName } }`, nil)
	if out["errors"] != nil {
		t.Fatalf("updateAccount: %v", out)
	}
	if p := dig(t, anon.gql(profileGQL, map[string]any{"h": handle}), "data", "profile"); p != nil {
		t.Fatalf("private profile visible: %v", p)
	}
	if p := dig(t, alice.gql(profileGQL, map[string]any{"h": handle}), "data", "profile"); p == nil {
		t.Fatal("owner lost her private profile")
	}
	e := dig(t, anon.gql(boardQ, nil), "data", "leaderboard", "entries").([]any)[0].(map[string]any)
	if e["displayName"] != service.PrivatePlayerName || e["handle"] != nil {
		t.Fatalf("private entry = %v", e)
	}
	alice.gql(`mutation { updateAccount(input: {profilePublic: true}) { profilePublic } }`, nil)

	// Sign out; the cookie is dead; signing in again gives the same account.
	if dig(t, alice.gql(`mutation { logout }`, nil), "data", "logout") != true {
		t.Fatal("logout failed")
	}
	if dig(t, alice.gql(meQ, nil), "data", "me") != nil {
		t.Fatal("still signed in after logout")
	}
	alice.signIn("alice", "/")
	if dig(t, alice.gql(meQ, nil), "data", "me", "handle") != handle {
		t.Fatal("a second sign-in made a different account")
	}

	// A guest plays in another browser, then signs in and claims the run.
	guest := newBrowser(t, ts.URL)
	gid, gtoken := guest.play("GuestG")
	if board := guest.gql(boardQ, map[string]any{"sid": gid}); dig(t, board, "data", "leaderboard", "total") != float64(2) {
		t.Fatalf("board = %v", board)
	}
	guest.signIn("grace", "/")
	res := dig(t, guest.gql(claimQ, map[string]any{"c": []map[string]any{
		{"sessionID": gid, "ownerToken": gtoken},
		{"sessionID": gid, "ownerToken": "wrong"},
		{"sessionID": id, "ownerToken": gtoken}, // someone else's session
		{"sessionID": "0000000000000000", "ownerToken": gtoken},
	}}), "data", "claimSessions").([]any)
	var statuses []string
	for _, r := range res {
		statuses = append(statuses, r.(map[string]any)["status"].(string))
	}
	if got := strings.Join(statuses, ","); got != "ATTACHED,ALREADY_YOURS,REJECTED,REJECTED" {
		// The second claim carries a wrong token for a now-claimed session of the same account.
		if got != "ATTACHED,REJECTED,REJECTED,REJECTED" {
			t.Fatalf("claim statuses = %s", got)
		}
	}
	// Alice cannot take Grace's session even with its token (re-attach is refused).
	res = dig(t, alice.gql(claimQ, map[string]any{"c": []map[string]any{{"sessionID": gid, "ownerToken": gtoken}}}), "data", "claimSessions").([]any)
	if res[0].(map[string]any)["status"] != "REJECTED" {
		t.Fatalf("re-attach = %v", res)
	}
	// Anonymous claims need a sign-in.
	if code := firstErrCode(anon.gql(claimQ, map[string]any{"c": []map[string]any{{"sessionID": gid, "ownerToken": gtoken}}})); code != service.CodeUnauthenticated {
		t.Fatalf("anonymous claim code = %q", code)
	}
	graceHandle := dig(t, guest.gql(meQ, nil), "data", "me", "handle").(string)
	if e := dig(t, anon.gql(boardQ, nil), "data", "leaderboard", "entries").([]any); len(e) != 2 || !hasEntry(e, graceHandle) {
		t.Fatalf("board after claim = %v", e)
	}

	// Delete: the wrong confirmation is refused, the right one removes everything.
	if code := firstErrCode(alice.gql(`mutation { deleteAccount(confirmHandle: "nope") }`, nil)); code != service.CodeInvalidArgument {
		t.Fatalf("wrong confirmation code = %q", code)
	}
	if dig(t, alice.gql(meQ, nil), "data", "me") == nil {
		t.Fatal("a refused delete signed the player out")
	}
	if out = alice.gql(`mutation($h: String!) { deleteAccount(confirmHandle: $h) }`, map[string]any{"h": strings.ToUpper(handle)}); out["errors"] != nil {
		t.Fatalf("deleteAccount: %v", out)
	}
	if dig(t, alice.gql(meQ, nil), "data", "me") != nil {
		t.Fatal("still signed in after deleting the account")
	}
	if p := dig(t, anon.gql(profileGQL, map[string]any{"h": handle}), "data", "profile"); p != nil {
		t.Fatal("profile survived deletion")
	}
	after := dig(t, anon.gql(boardQ, nil), "data", "leaderboard", "entries").([]any)
	for _, e := range after {
		if e.(map[string]any)["sessionId"] == id {
			if e.(map[string]any)["handle"] != nil || e.(map[string]any)["displayName"] == handle || e.(map[string]any)["displayName"] == "Real Name" {
				t.Fatalf("entry after deletion still identifies the account: %v", e)
			}
		}
	}
	if len(after) != 2 {
		t.Fatalf("deleted account's run vanished or duplicated: %v", after)
	}
	// Her old cookie is dead; the owner token of her session is still hers to use
	// only through a signed-in claim (not exercised here).
	_ = token
}

func hasEntry(entries []any, handle string) bool {
	for _, e := range entries {
		if e.(map[string]any)["handle"] == handle {
			return true
		}
	}
	return false
}

func mustURL(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		panic(err)
	}
	return u
}

// dumpSessions is only used by the test above: it asks the service for every
// session via the public store the test controls (no file persistence is
// configured here, so this inspects the in-memory snapshot's hash instead).
func (s *acctServer) dumpSessions(t *testing.T) string {
	t.Helper()
	list, _ := s.svc.ListSessions(service.SessionFilter{Limit: 500})
	var b strings.Builder
	for _, x := range list {
		b.WriteString(x.ID + " " + x.AccountID + " " + x.OwnerHash + "\n")
	}
	return b.String()
}

// A page on another site, or a form post, must never act as the signed-in player.
func TestCookieIsIgnoredOnCrossSiteRequests(t *testing.T) {
	ts := newAccountsServer(t, "")
	b := newBrowser(t, ts.URL)
	b.signIn("alice", "/")
	if dig(t, b.gql(meQ, nil), "data", "me") == nil {
		t.Fatal("not signed in")
	}
	tests := []struct {
		name     string
		override map[string]string
		wantMe   bool
	}{
		{"same origin", nil, true},
		{"foreign origin", map[string]string{"Origin": "https://evil.example"}, false},
		{"foreign origin with fetch metadata", map[string]string{"Origin": "https://evil.example", "Sec-Fetch-Site": "cross-site"}, false},
		{"origin null (sandboxed iframe)", map[string]string{"Origin": "null"}, false},
		{"no origin but cross-site metadata", map[string]string{"Origin": "", "Sec-Fetch-Site": "cross-site"}, false},
		{"plain-text body (a cross-site 'simple' request)", map[string]string{"Content-Type": "text/plain"}, false},
		{"form body", map[string]string{"Content-Type": "application/x-www-form-urlencoded"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, out := b.gqlWith(meQ, nil, tc.override)
			got := out != nil && out["data"] != nil && dig(t, out, "data", "me") != nil // non-JSON bodies are refused before any resolver runs
			if got != tc.wantMe {
				t.Fatalf("me visible = %v, want %v (out %v)", got, tc.wantMe, out)
			}
		})
	}
	// A state-changing call from a foreign origin fails as unauthenticated, and changes nothing.
	_, out := b.gqlWith(`mutation { updateAccount(input: {profilePublic: false}) { handle } }`, nil, map[string]string{"Origin": "https://evil.example"})
	if firstErrCode(out) != service.CodeUnauthenticated {
		t.Fatalf("cross-site mutation = %v", out)
	}
	_, out = b.gqlWith(`mutation { deleteAccount(confirmHandle: "x") }`, nil, map[string]string{"Origin": "https://evil.example"})
	if firstErrCode(out) != service.CodeUnauthenticated {
		t.Fatalf("cross-site delete = %v", out)
	}
	if dig(t, b.gql(meQ, nil), "data", "me", "profilePublic") != true {
		t.Fatal("a cross-site request changed the account")
	}
	// Logout over a foreign origin is a no-op, not a refusal: the viewer is never
	// resolved for a cross-site request, so there is nothing to sign out. The
	// mutation still answers true, as the schema promises it always does, and the
	// login survives.
	_, out = b.gqlWith(`mutation { logout }`, nil, map[string]string{"Origin": "https://evil.example"})
	if dig(t, out, "data", "logout") != true {
		t.Fatalf("cross-site logout = %v, want true", out)
	}
	if dig(t, b.gql(meQ, nil), "data", "me") == nil {
		t.Fatal("a cross-site request signed the player out")
	}
}

// CORS must never be combined with credentials, whatever ALLOWED_ORIGINS says.
func TestCORSNeverAllowsCredentials(t *testing.T) {
	for _, allowed := range []string{"", "*", "https://evil.example"} {
		t.Run("ALLOWED_ORIGINS="+allowed, func(t *testing.T) {
			ts := newAccountsServer(t, allowed)
			for _, method := range []string{http.MethodPost, http.MethodOptions} {
				req, _ := http.NewRequest(method, ts.URL+"/graphql", strings.NewReader(`{"query":"{ me { handle } }"}`))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Origin", "https://evil.example")
				if method == http.MethodOptions {
					req.Header.Set("Access-Control-Request-Method", "POST")
				}
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				resp.Body.Close()
				if v := resp.Header.Get("Access-Control-Allow-Credentials"); v != "" {
					t.Fatalf("%s: Access-Control-Allow-Credentials = %q", method, v)
				}
			}
		})
	}
}

func TestAccountMutationsNeedSignIn(t *testing.T) {
	ts := newAccountsServer(t, "")
	anon := newBrowser(t, ts.URL)
	for _, q := range []string{
		`mutation { updateAccount(input: {displayName: "x"}) { handle } }`,
		`mutation { deleteAccount(confirmHandle: "x") }`,
		`mutation { claimSessions(claims: []) { status } }`,
	} {
		if code := firstErrCode(anon.gql(q, nil)); code != service.CodeUnauthenticated {
			t.Errorf("%s -> %q", q, code)
		}
	}
	if dig(t, anon.gql(`mutation { logout }`, nil), "data", "logout") != true {
		t.Fatal("logout while signed out should be harmless")
	}
}

func TestHandleRulesOverHTTP(t *testing.T) {
	ts := newAccountsServer(t, "")
	a, b := newBrowser(t, ts.URL), newBrowser(t, ts.URL)
	a.signIn("alice", "/")
	b.signIn("bob", "/")
	bHandle := dig(t, b.gql(meQ, nil), "data", "me", "handle").(string)
	for _, bad := range []string{"ab", "Has Space", "admin", "fuck_you", strings.ToUpper(bHandle)} {
		out := a.gql(`mutation($h: String!) { updateAccount(input: {handle: $h}) { handle } }`, map[string]any{"h": bad})
		if firstErrCode(out) != service.CodeInvalidArgument {
			t.Errorf("handle %q -> %v", bad, out)
		}
	}
	if out := a.gql(`mutation { updateAccount(input: {handle: "Fast_Fox"}) { handle handleChangeableAt } }`, nil); dig(t, out, "data", "updateAccount", "handle") != "fast_fox" || dig(t, out, "data", "updateAccount", "handleChangeableAt") == nil {
		t.Fatalf("update = %v", out)
	}
	if code := firstErrCode(a.gql(`mutation { updateAccount(input: {handle: "other_fox"}) { handle } }`, nil)); code != service.CodeLimit {
		t.Fatalf("second change code = %q", code)
	}
}

// The feature is off unless a provider is configured: no routes, no viewer, the
// same behaviour as before accounts existed.
func TestNoProvidersLeavesTheAppUnchanged(t *testing.T) {
	ts := newRealServer(t) // no Auth, no account store
	b := newBrowser(t, ts.URL)

	for _, p := range []string{"/auth/google/login", "/auth/dev/login", "/auth/google/callback"} {
		for _, m := range []string{http.MethodGet, http.MethodPost} {
			req, _ := http.NewRequest(m, ts.URL+p, nil)
			resp, _ := b.do(req)
			if resp.StatusCode != http.StatusNotFound {
				t.Errorf("%s %s = %d, want 404", m, p, resp.StatusCode)
			}
			if len(resp.Header.Values("Set-Cookie")) != 0 {
				t.Errorf("%s %s sets a cookie", m, p)
			}
		}
	}
	if dig(t, b.gql(meQ, nil), "data", "me") != nil {
		t.Fatal("me is not null")
	}
	if provs := dig(t, b.gql(`{ authProviders { id } }`, nil), "data", "authProviders").([]any); len(provs) != 0 {
		t.Fatalf("authProviders = %v", provs)
	}
	if p := dig(t, b.gql(profileGQL, map[string]any{"h": "anyone"}), "data", "profile"); p != nil {
		t.Fatalf("profile = %v", p)
	}
	// Guest play, exactly as an agent does it: createSession and read `id`.
	resp, out := b.gqlWith(`mutation { createSession(mapID: "wide") { id displayName } }`, nil, nil)
	if out["errors"] != nil || dig(t, out, "data", "createSession", "id") == "" {
		t.Fatalf("createSession: %v", out)
	}
	if len(resp.Header.Values("Set-Cookie")) != 0 {
		t.Fatal("guest play sets a cookie")
	}
	id, _ := b.play("Guest")
	if board := b.gql(boardQ, map[string]any{"sid": id}); dig(t, board, "data", "leaderboard", "total") != float64(1) {
		t.Fatalf("board = %v", board)
	}
	for _, q := range []string{`mutation { updateAccount(input: {displayName: "x"}) { handle } }`, `mutation { claimSessions(claims: []) { status } }`} {
		if code := firstErrCode(b.gql(q, nil)); code != service.CodeUnauthenticated {
			t.Errorf("%s -> %q", q, code)
		}
	}
	// A stray login cookie is not even looked at.
	b.headers["Cookie"] = auth.SessionCookie + "=0123456789012345678901234567890123456789"
	if dig(t, b.gql(meQ, nil), "data", "me") != nil {
		t.Fatal("cookie honoured with sign-in off")
	}
}
