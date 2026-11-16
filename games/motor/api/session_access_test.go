package api

import (
	"testing"

	"github.com/wricardo/drbrain-motor-programming/game/service"
)

const (
	deleteQ = `mutation($id: ID!, $t: String) { deleteSession(id: $id, ownerToken: $t) }`
	renameQ = `mutation($id: ID!, $n: String!, $t: String) { renameSession(id: $id, displayName: $n, ownerToken: $t) { displayName } }`
)

// Anyone who knows a session id (it is in the public watch link) must not be able
// to delete or rename a signed-in player's run; guest sessions stay open as before.
func TestOnlyTheOwnerCanDeleteOrRenameASignedInPlayersSession(t *testing.T) {
	ts := newAccountsServer(t, "")
	alice, anon := newBrowser(t, ts.URL), newBrowser(t, ts.URL)
	alice.signIn("alice", "/")

	out := alice.gql(createSessionQ, map[string]any{"name": "Alice's run"})
	id := dig(t, out, "data", "createSession", "id").(string)
	token := dig(t, out, "data", "createSession", "ownerToken").(string)

	// An anonymous caller (an agent, a spectator, a vandal) is refused, with or without a wrong token.
	for name, vars := range map[string]map[string]any{
		"no token":    {"id": id},
		"wrong token": {"id": id, "t": "not-the-token"},
		"empty token": {"id": id, "t": ""},
	} {
		if code := firstErrCode(anon.gql(deleteQ, vars)); code != service.CodeForbidden {
			t.Fatalf("delete (%s): code %q", name, code)
		}
		rv := map[string]any{"n": "owned by me now"}
		for k, v := range vars {
			rv[k] = v
		}
		if code := firstErrCode(anon.gql(renameQ, rv)); code != service.CodeForbidden {
			t.Fatalf("rename (%s): code %q", name, code)
		}
	}
	if dig(t, anon.gql(`query($id: ID!) { session(id: $id) { displayName } }`, map[string]any{"id": id}), "data", "session", "displayName") != "Alice's run" {
		t.Fatal("a refused request still changed the session")
	}

	// A foreign site cannot use the owner's cookie to do it either.
	if _, o := alice.gqlWith(deleteQ, map[string]any{"id": id}, map[string]string{"Origin": "https://evil.example"}); firstErrCode(o) != service.CodeForbidden {
		t.Fatalf("cross-site delete = %v", o)
	}

	// Another signed-in player is a stranger too.
	bob := newBrowser(t, ts.URL)
	bob.signIn("bob", "/")
	if code := firstErrCode(bob.gql(deleteQ, map[string]any{"id": id})); code != service.CodeForbidden {
		t.Fatalf("another account: code %q", code)
	}
	if code := firstErrCode(bob.gql(setProgramQ, map[string]any{"id": id, "p": winningProgram()})); code != service.CodeForbidden {
		t.Fatalf("another account playing: code %q", code)
	}

	// The secret token is the way to play someone else's session (how a run is
	// handed to a second person or an AI agent) — and so is the owner's cookie.
	if out := anon.gql(setProgramQ, map[string]any{"id": id, "p": winningProgram(), "t": token}); out["errors"] != nil {
		t.Fatalf("an agent with the owner token could not play: %v", out)
	}
	if out := alice.gql(setProgramQ, map[string]any{"id": id, "p": winningProgram()}); out["errors"] != nil {
		t.Fatalf("the owner could not play their own session: %v", out)
	}

	// The secret token works for someone without the cookie, and the owner's cookie works.
	if out := anon.gql(renameQ, map[string]any{"id": id, "n": "Renamed", "t": token}); dig(t, out, "data", "renameSession", "displayName") != "Renamed" {
		t.Fatalf("rename with the token: %v", out)
	}
	if out := alice.gql(renameQ, map[string]any{"id": id, "n": "Renamed again"}); dig(t, out, "data", "renameSession", "displayName") != "Renamed again" {
		t.Fatalf("rename by the owner: %v", out)
	}
	if out := alice.gql(deleteQ, map[string]any{"id": id}); dig(t, out, "data", "deleteSession") != true {
		t.Fatalf("delete by the owner: %v", out)
	}
	if dig(t, anon.gql(`query($id: ID!) { session(id: $id) { id } }`, map[string]any{"id": id}), "data", "session") != nil {
		t.Fatal("the session survived its owner's delete")
	}
}

// Deleting or renaming a guest session still needs nothing: agents clean up by
// id, and only playing is creator-only.
func TestGuestSessionsCanStillBeDeletedAndRenamedByAnyone(t *testing.T) {
	ts := newAccountsServer(t, "")
	guest, other := newBrowser(t, ts.URL), newBrowser(t, ts.URL)
	out := guest.gql(createSessionQ, map[string]any{"name": "G"})
	id := dig(t, out, "data", "createSession", "id").(string)

	// Existing agents call deleteSession(id) and renameSession(id, name) with nothing else.
	if out := other.gql(`mutation($id: ID!) { renameSession(id: $id, displayName: "Bot") { displayName } }`, map[string]any{"id": id}); dig(t, out, "data", "renameSession", "displayName") != "Bot" {
		t.Fatalf("rename: %v", out)
	}
	if out := other.gql(`mutation($id: ID!) { deleteSession(id: $id) }`, map[string]any{"id": id}); dig(t, out, "data", "deleteSession") != true {
		t.Fatalf("delete: %v", out)
	}
	// Deleting something that does not exist is still NOT_FOUND, not FORBIDDEN.
	if code := firstErrCode(other.gql(deleteQ, map[string]any{"id": "00000000deadbeef"})); code != service.CodeNotFound {
		t.Fatalf("unknown session: code %q", code)
	}
}

// A guest (no account at all) is the creator of their session: the owner token
// is the only proof, and without it nobody else can play.
func TestOnlyTheCreatorCanPlayAGuestsSession(t *testing.T) {
	ts := newAccountsServer(t, "")
	creator, stranger := newBrowser(t, ts.URL), newBrowser(t, ts.URL)
	out := creator.gql(createSessionQ, map[string]any{"name": "G"})
	id := dig(t, out, "data", "createSession", "id").(string)
	token := dig(t, out, "data", "createSession", "ownerToken").(string)

	// The creator plays with the token it was given (this browser keeps it in localStorage).
	if out := creator.gql(setProgramQ, map[string]any{"id": id, "p": winningProgram(), "t": token}); out["errors"] != nil {
		t.Fatalf("creator could not play: %v", out)
	}
	if out := creator.gql(stepQ, map[string]any{"id": id, "t": token}); out["errors"] != nil {
		t.Fatalf("creator could not step: %v", out)
	}

	// A stranger holding only the public id cannot play it at all.
	for name, vars := range map[string]map[string]any{
		"no token":    {"id": id},
		"wrong token": {"id": id, "t": "not-the-token"},
		"empty token": {"id": id, "t": ""},
	} {
		for _, q := range []string{setProgramQ, stepQ} {
			v := map[string]any{"p": winningProgram()}
			for k, val := range vars {
				v[k] = val
			}
			if code := firstErrCode(stranger.gql(q, v)); code != service.CodeForbidden {
				t.Fatalf("%s (%s): code %q", q, name, code)
			}
		}
	}
	// Every play mutation is gated, not just setProgram/step.
	for _, q := range []string{
		`mutation($id: ID!) { run(sessionID: $id) { id } }`,
		`mutation($id: ID!) { pause(sessionID: $id) { id } }`,
		`mutation($id: ID!) { reset(sessionID: $id) { id } }`,
	} {
		if code := firstErrCode(stranger.gql(q, map[string]any{"id": id})); code != service.CodeForbidden {
			t.Fatalf("%s: code %q", q, code)
		}
	}

	// Sharing the token is the way to let a second player in, and it does not
	// stop working for the creator.
	if out := stranger.gql(setProgramQ, map[string]any{"id": id, "p": winningProgram(), "t": token}); out["errors"] != nil {
		t.Fatalf("a shared session could not be played: %v", out)
	}

	// Watching stays open: the public id is enough to read the session.
	if s := dig(t, stranger.gql(`query($id: ID!) { session(id: $id) { id displayName } }`, map[string]any{"id": id}), "data", "session"); s == nil {
		t.Fatal("a stranger could not spectate")
	}
}

// canPlaySession lets a play page decide between playing and a read-only view
// without provoking a FORBIDDEN on the first save.
func TestCanPlaySessionReflectsOwnership(t *testing.T) {
	ts := newAccountsServer(t, "")
	creator, stranger := newBrowser(t, ts.URL), newBrowser(t, ts.URL)
	out := creator.gql(createSessionQ, map[string]any{"name": "G"})
	id := dig(t, out, "data", "createSession", "id").(string)
	token := dig(t, out, "data", "createSession", "ownerToken").(string)
	q := `query($id: ID!, $t: String) { canPlaySession(id: $id, ownerToken: $t) }`

	for name, tc := range map[string]struct {
		b    *browser
		vars map[string]any
		want bool
	}{
		"creator with the token": {creator, map[string]any{"id": id, "t": token}, true},
		"creator without it":     {creator, map[string]any{"id": id}, false},
		"stranger with it":       {stranger, map[string]any{"id": id, "t": token}, true},
		"stranger without it":    {stranger, map[string]any{"id": id}, false},
		"stranger, wrong token":  {stranger, map[string]any{"id": id, "t": "nope"}, false},
		"unknown session":        {stranger, map[string]any{"id": "00000000deadbeef"}, false},
	} {
		if got := dig(t, tc.b.gql(q, tc.vars), "data", "canPlaySession"); got != tc.want {
			t.Fatalf("%s: canPlaySession = %v, want %v", name, got, tc.want)
		}
	}
}

// The signed-in player's own browser can delete a session it claimed from the guest days.
func TestClaimedSessionsBecomeProtected(t *testing.T) {
	ts := newAccountsServer(t, "")
	b, anon := newBrowser(t, ts.URL), newBrowser(t, ts.URL)
	// A guest session is open to anyone until it is claimed.
	guestOut := b.gql(createSessionQ, map[string]any{"name": "G"})
	guestID := dig(t, guestOut, "data", "createSession", "id").(string)
	if code := firstErrCode(anon.gql(deleteQ, map[string]any{"id": guestID})); code != "" {
		t.Fatalf("a guest session was protected before being claimed: %q", code)
	}

	out := b.gql(createSessionQ, map[string]any{"name": "G2"}) // also created as a guest
	id := dig(t, out, "data", "createSession", "id").(string)
	token := dig(t, out, "data", "createSession", "ownerToken").(string)
	b.signIn("alice", "/")
	if res := dig(t, b.gql(claimQ, map[string]any{"c": []map[string]any{{"sessionID": id, "ownerToken": token}}}), "data", "claimSessions").([]any); res[0].(map[string]any)["status"] != "ATTACHED" {
		t.Fatalf("claim = %v", res)
	}
	if code := firstErrCode(anon.gql(deleteQ, map[string]any{"id": id})); code != service.CodeForbidden {
		t.Fatalf("claimed session not protected: %q", code)
	}
	if out := b.gql(deleteQ, map[string]any{"id": id}); dig(t, out, "data", "deleteSession") != true {
		t.Fatalf("owner delete: %v", out)
	}
}
