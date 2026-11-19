package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	gorillaws "github.com/gorilla/websocket"
)

const (
	sessionProgramQ   = `query($id: ID!, $t: String) { session(id: $id, ownerToken: $t) { id program { main subs } } }`
	sessionsProgramQ  = `{ sessions(limit: 50) { id program { main subs } } }`
	renameProgramQ    = `mutation($id: ID!, $n: String!) { renameSession(id: $id, displayName: $n) { id program { main subs } } }`
	sessionUpdatedSub = `subscription($id: ID!, $t: String) { sessionUpdated(sessionID: $id, ownerToken: $t) { seq session { id program { main subs } } } }`
)

func programOf(t *testing.T, out map[string]any, path ...string) any {
	t.Helper()
	v := dig(t, out, path...)
	session, _ := v.(map[string]any)
	if session == nil {
		t.Fatalf("no session at %v: %v", path, out)
	}
	return session["program"]
}

// A session id is public (it is the watch link), so the program must leave the
// server only to someone who may change it. Otherwise a watch link is an answer
// key. Driving playback is open to anyone and must not hand it out either.
func TestTheProgramIsVisibleOnlyToPlayers(t *testing.T) {
	srv := newAccountsServer(t, "")
	owner, anon := newBrowser(t, srv.URL), newBrowser(t, srv.URL)

	out := owner.gql(createSessionQ, map[string]any{"name": "Private"})
	id := dig(t, out, "data", "createSession", "id").(string)
	token := dig(t, out, "data", "createSession", "ownerToken").(string)
	if out := owner.gql(setProgramQ, map[string]any{"id": id, "p": winningProgram(), "t": token}); out["errors"] != nil {
		t.Fatalf("setProgram: %v", out)
	}

	// The player (owner token) reads it back.
	got := programOf(t, owner.gql(sessionProgramQ, map[string]any{"id": id, "t": token}), "data", "session")
	if got == nil {
		t.Fatal("the owner cannot read the program")
	}
	main, _ := got.(map[string]any)["main"].([]any)
	if len(main) != 8 || main[0] != "MOVE_FORWARD" {
		t.Fatalf("owner program = %v", got)
	}

	// A spectator with the public id gets no program, with no token or a wrong one.
	for name, vars := range map[string]map[string]any{
		"no token":    {"id": id},
		"wrong token": {"id": id, "t": "not-the-token"},
		"empty token": {"id": id, "t": ""},
	} {
		if got := programOf(t, anon.gql(sessionProgramQ, vars), "data", "session"); got != nil {
			t.Fatalf("session query (%s) leaked the program: %v", name, got)
		}
	}

	// A guest session may be renamed by anyone with its id, so the response of
	// that open mutation must not carry the program either.
	if got := programOf(t, anon.gql(renameProgramQ, map[string]any{"id": id, "n": "Renamed by a spectator"}), "data", "renameSession"); got != nil {
		t.Fatalf("renameSession leaked the program: %v", got)
	}

	// The session lists never carry programs, not even for the player.
	out = owner.gql(sessionsProgramQ, nil)
	rows, _ := dig(t, out, "data", "sessions").([]any)
	if len(rows) == 0 {
		t.Fatalf("no sessions listed: %v", out)
	}
	for _, row := range rows {
		if p, ok := row.(map[string]any)["program"]; ok && p != nil {
			t.Fatalf("sessions list leaked a program: %v", row)
		}
	}

	// A signed-in owner needs no token: their cookie is the credential.
	alice := newBrowser(t, srv.URL)
	alice.signIn("alice", "/")
	out = alice.gql(createSessionQ, map[string]any{"name": "Alice's"})
	ownedID := dig(t, out, "data", "createSession", "id").(string)
	if out := alice.gql(setProgramQ, map[string]any{"id": ownedID, "p": winningProgram()}); out["errors"] != nil {
		t.Fatalf("setProgram as the owner: %v", out)
	}
	if got := programOf(t, alice.gql(sessionProgramQ, map[string]any{"id": ownedID}), "data", "session"); got == nil {
		t.Fatal("the signed-in owner cannot read the program")
	}
	stranger := newBrowser(t, srv.URL)
	if got := programOf(t, stranger.gql(sessionProgramQ, map[string]any{"id": ownedID}), "data", "session"); got != nil {
		t.Fatalf("a stranger read a signed-in player's program: %v", got)
	}
}

// The live view is a subscription, so it must hide the program too.
func TestSubscriptionHidesTheProgramFromSpectators(t *testing.T) {
	srv := newAccountsServer(t, "")
	owner := newBrowser(t, srv.URL)
	out := owner.gql(createSessionQ, map[string]any{"name": "Live"})
	id := dig(t, out, "data", "createSession", "id").(string)
	token := dig(t, out, "data", "createSession", "ownerToken").(string)
	if out := owner.gql(setProgramQ, map[string]any{"id": id, "p": winningProgram(), "t": token}); out["errors"] != nil {
		t.Fatalf("setProgram: %v", out)
	}

	type wsCase struct {
		name    string
		vars    map[string]any
		wantNil bool
	}
	cases := []wsCase{
		{"spectator", map[string]any{"id": id}, true},
		{"wrong token", map[string]any{"id": id, "t": "nope"}, true},
		{"owner", map[string]any{"id": id, "t": token}, false},
	}
	for _, c := range cases {
		conn := dialWS(t, srv.Server)
		_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		if err := conn.WriteJSON(map[string]any{"type": "connection_init"}); err != nil {
			t.Fatal(err)
		}
		if _, _, err := conn.ReadMessage(); err != nil {
			t.Fatal(err)
		}
		if err := conn.WriteJSON(map[string]any{"id": "1", "type": "subscribe", "payload": map[string]any{"query": sessionUpdatedSub, "variables": c.vars}}); err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		for i := 0; i < 5; i++ {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				t.Fatalf("%s: read: %v", c.name, err)
			}
			var env struct {
				Type    string         `json:"type"`
				Payload map[string]any `json:"payload"`
			}
			if err := json.Unmarshal(msg, &env); err != nil {
				t.Fatalf("%s: bad message %q", c.name, msg)
			}
			if env.Type == "next" {
				payload = env.Payload
				break
			}
			if env.Type == "error" {
				t.Fatalf("%s: subscription error %q", c.name, msg)
			}
			if !strings.Contains(env.Type, "ack") && env.Type != "ping" && env.Type != "pong" {
				t.Fatalf("%s: unexpected message %q", c.name, msg)
			}
		}
		if payload == nil {
			t.Fatalf("%s: no data payload", c.name)
		}
		update, _ := payload["data"].(map[string]any)["sessionUpdated"].(map[string]any)
		session, _ := update["session"].(map[string]any)
		got := session["program"]
		if c.wantNil && got != nil {
			t.Fatalf("%s: subscription leaked the program: %v", c.name, got)
		}
		if !c.wantNil && got == nil {
			t.Fatalf("%s: subscription omitted the owner's program", c.name)
		}
	}
}

// The subscription is the live view, and its program gate is the caller's
// login too: a signed-in owner must receive their own program over the
// websocket upgrade, where the cookie (not an ownerToken) proves it. The
// schema documents "ownerToken or the owning login" for sessionUpdated.
func TestSubscriptionShowsTheProgramToTheSignedInOwner(t *testing.T) {
	srv := newAccountsServer(t, "")
	alice := newBrowser(t, srv.URL)
	alice.signIn("alice", "/")
	out := alice.gql(createSessionQ, map[string]any{"name": "Alice's live"})
	id := dig(t, out, "data", "createSession", "id").(string)
	if out := alice.gql(setProgramQ, map[string]any{"id": id, "p": winningProgram()}); out["errors"] != nil {
		t.Fatalf("setProgram as the owner: %v", out)
	}

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	var cookies []string
	for _, c := range alice.client.Jar.Cookies(u) {
		cookies = append(cookies, c.Name+"="+c.Value)
	}
	if len(cookies) == 0 {
		t.Fatal("sign-in left no cookie in the jar")
	}
	h := http.Header{}
	h.Set("Cookie", strings.Join(cookies, "; "))
	h.Set("Origin", srv.URL)
	d := gorillaws.Dialer{Subprotocols: []string{"graphql-transport-ws"}, HandshakeTimeout: 5 * time.Second}
	c, _, err := d.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/graphql", h)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
	if err := c.WriteJSON(map[string]any{"type": "connection_init"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.ReadMessage(); err != nil {
		t.Fatal(err)
	}
	// No ownerToken: only the login cookie can authorize the program here.
	if err := c.WriteJSON(map[string]any{"id": "1", "type": "subscribe", "payload": map[string]any{
		"query": sessionUpdatedSub, "variables": map[string]any{"id": id},
	}}); err != nil {
		t.Fatal(err)
	}
	for range 5 {
		_, msg, err := c.ReadMessage()
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		var env struct {
			Type    string         `json:"type"`
			Payload map[string]any `json:"payload"`
		}
		if err := json.Unmarshal(msg, &env); err != nil {
			t.Fatalf("bad message %q", msg)
		}
		if env.Type == "error" {
			t.Fatalf("subscription error %q", msg)
		}
		if env.Type != "next" {
			continue
		}
		update, _ := env.Payload["data"].(map[string]any)["sessionUpdated"].(map[string]any)
		session, _ := update["session"].(map[string]any)
		if got := session["program"]; got == nil {
			t.Fatal("the signed-in owner's program is missing from the subscription snapshot")
		}
		return
	}
	t.Fatal("no data payload")
}
