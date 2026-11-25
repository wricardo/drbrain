package graph

import (
	"net/http/httptest"
	"testing"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/extension"
	"github.com/99designs/gqlgen/graphql/handler/transport"

	"github.com/wricardo/drbrain/games/delivery/game/session"
	"github.com/wricardo/drbrain/games/delivery/graph/generated"
	"github.com/wricardo/drbrain/platform/pgdb/pgtest"
)

// newAdminTestServer is testHandler plus the admin key, which no other test
// needs configured.
func newAdminTestServer(t *testing.T, adminKey string) *httptest.Server {
	t.Helper()
	pool := pgtest.Pool(t)
	cat := newCityCatalog(t, pool)
	mgr, err := session.NewManager(cat, pool)
	if err != nil {
		t.Fatalf("manager: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Close() })

	srv := handler.New(generated.NewExecutableSchema(generated.Config{
		Resolvers: &Resolver{Mgr: mgr, Catalog: cat, AdminKey: adminKey},
	}))
	srv.AddTransport(transport.POST{})
	srv.Use(extension.Introspection{})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return ts
}

// TestAdminDeleteSession covers the admin gate and the ownership bypass: a
// wrong key is FORBIDDEN and deletes nothing, the configured key deletes
// another player's save, and a second delete is a no-op success.
func TestAdminDeleteSession(t *testing.T) {
	ts := newAdminTestServer(t, "sekret")
	createSession(t, ts, "admin-del-1", 3)

	res := query(t, ts, `mutation { admin(key: "wrong") { deleteSession(id: "admin-del-1") } }`, nil)
	if len(res.Errors) == 0 {
		t.Fatal("wrong admin key was accepted")
	}
	if got := res.Errors[0].Extensions["code"]; got != codeForbidden {
		t.Fatalf("wrong-key code = %v, want %s", got, codeForbidden)
	}

	// The failed attempt must leave the save readable.
	var stillThere struct {
		Session struct{ ID string }
	}
	mustQuery(t, ts, `{ session(id: "admin-del-1") { id } }`, nil, &stillThere)
	if stillThere.Session.ID != "admin-del-1" {
		t.Fatalf("session disappeared after a rejected key: %+v", stillThere)
	}

	var out struct {
		Admin struct{ DeleteSession bool }
	}
	mustQuery(t, ts, `mutation { admin(key: "sekret") { deleteSession(id: "admin-del-1") } }`, nil, &out)
	if !out.Admin.DeleteSession {
		t.Fatal("admin delete returned false")
	}

	res = query(t, ts, `{ session(id: "admin-del-1") { id } }`, nil)
	if len(res.Errors) == 0 {
		t.Fatal("session still readable after admin delete")
	}
	if got := res.Errors[0].Extensions["code"]; got != codeNotFound {
		t.Fatalf("post-delete code = %v, want %s", got, codeNotFound)
	}

	// Idempotent: deleting a missing save is still a success.
	mustQuery(t, ts, `mutation { admin(key: "sekret") { deleteSession(id: "admin-del-1") } }`, nil, &out)
	if !out.Admin.DeleteSession {
		t.Fatal("second admin delete returned false")
	}
}

// TestAdminDisabledWithoutKey keeps the no-key policy: admin is FORBIDDEN
// unless the local-development opt-out is set, and an empty key is only
// accepted with that opt-out.
func TestAdminDisabledWithoutKey(t *testing.T) {
	ts := newAdminTestServer(t, "")

	res := query(t, ts, `mutation { admin(key: "") { deleteSession(id: "nope") } }`, nil)
	if len(res.Errors) == 0 {
		t.Fatal("admin ran with no configured key")
	}
	if got := res.Errors[0].Extensions["code"]; got != codeForbidden {
		t.Fatalf("code = %v, want %s", got, codeForbidden)
	}
}

// TestAdminSessionLifecycle covers pause -> save -> saveAll -> evict. Evicting
// must keep the save readable, and pausing must be observable without the owner
// token.
func TestAdminSessionLifecycle(t *testing.T) {
	ts := newAdminTestServer(t, "sekret")
	token := createSession(t, ts, "admin-life-1", 5)

	// Player sets a running clock; admin must be able to stop it.
	var speed struct {
		SetSpeed struct{ Paused bool }
	}
	mustQuery(t, ts, `mutation ($id: ID!, $tok: String) {
		setSpeed(id: $id, speed: 4, ownerToken: $tok) { paused }
	}`, map[string]any{"id": "admin-life-1", "tok": token}, &speed)
	if speed.SetSpeed.Paused {
		t.Fatal("session should be running before the admin pause")
	}

	var paused struct {
		Admin struct {
			PauseSession struct {
				ID     string
				Paused bool
			}
		}
	}
	mustQuery(t, ts, `mutation { admin(key: "sekret") {
		pauseSession(id: "admin-life-1") { id paused }
	} }`, nil, &paused)
	if !paused.Admin.PauseSession.Paused || paused.Admin.PauseSession.ID != "admin-life-1" {
		t.Fatalf("pauseSession = %+v", paused.Admin.PauseSession)
	}

	var saved struct {
		Admin struct {
			SaveSession  bool
			SaveAll      int
			EvictSession bool
		}
	}
	mustQuery(t, ts, `mutation { admin(key: "sekret") {
		saveSession(id: "admin-life-1")
		saveAll
		evictSession(id: "admin-life-1")
	} }`, nil, &saved)
	if !saved.Admin.SaveSession {
		t.Error("saveSession returned false")
	}
	if saved.Admin.SaveAll != 1 {
		t.Errorf("saveAll = %d, want 1 live session", saved.Admin.SaveAll)
	}
	if !saved.Admin.EvictSession {
		t.Error("evictSession returned false")
	}

	// Eviction drops memory, not the save: the next read rehydrates it paused.
	var reloaded struct {
		Session struct {
			ID     string
			Paused bool
		}
	}
	mustQuery(t, ts, `{ session(id: "admin-life-1") { id paused } }`, nil, &reloaded)
	if reloaded.Session.ID != "admin-life-1" || !reloaded.Session.Paused {
		t.Fatalf("reloaded session = %+v", reloaded.Session)
	}

	// Stored-only sessions are no-ops for save/evict, and still success.
	mustQuery(t, ts, `mutation { admin(key: "sekret") {
		saveSession(id: "admin-life-1")
		evictSession(id: "admin-life-1")
	} }`, nil, &saved)
	if !saved.Admin.SaveSession || !saved.Admin.EvictSession {
		t.Fatalf("stored-only save/evict = %+v", saved.Admin)
	}
}

// TestAdminDeleteSessions checks the bulk result contract: duplicates collapse,
// existing ids land in deleted (live or stored) and unknown ids in missing.
func TestAdminDeleteSessions(t *testing.T) {
	ts := newAdminTestServer(t, "sekret")
	createSession(t, ts, "admin-bulk-1", 1)
	createSession(t, ts, "admin-bulk-2", 2)

	var out struct {
		Admin struct {
			DeleteSessions struct {
				Deleted []string
				Missing []string
			}
		}
	}
	mustQuery(t, ts, `mutation ($ids: [ID!]!) { admin(key: "sekret") {
		deleteSessions(ids: $ids) { deleted missing }
	} }`, map[string]any{"ids": []string{"admin-bulk-1", "ghost", "admin-bulk-2", "admin-bulk-1"}}, &out)

	got := out.Admin.DeleteSessions
	if len(got.Deleted) != 2 || got.Deleted[0] != "admin-bulk-1" || got.Deleted[1] != "admin-bulk-2" {
		t.Fatalf("deleted = %v, want [admin-bulk-1 admin-bulk-2]", got.Deleted)
	}
	if len(got.Missing) != 1 || got.Missing[0] != "ghost" {
		t.Fatalf("missing = %v, want [ghost]", got.Missing)
	}

	for _, id := range []string{"admin-bulk-1", "admin-bulk-2"} {
		res := query(t, ts, `query ($id: ID!) { session(id: $id) { id } }`, map[string]any{"id": id})
		if len(res.Errors) == 0 {
			t.Fatalf("session %s still readable after bulk delete", id)
		}
	}
}

// TestAdminDeleteSessionsRejectsBadInput keeps validation ahead of deletion:
// one invalid id fails the whole batch instead of deleting the rest.
func TestAdminDeleteSessionsRejectsBadInput(t *testing.T) {
	ts := newAdminTestServer(t, "sekret")
	createSession(t, ts, "admin-bad-1", 1)

	res := query(t, ts, `mutation { admin(key: "sekret") {
		deleteSessions(ids: ["admin-bad-1", "bad/id"]) { deleted missing }
	} }`, nil)
	if len(res.Errors) == 0 {
		t.Fatal("invalid id was accepted")
	}
	if got := res.Errors[0].Extensions["code"]; got != codeInvalidArgument {
		t.Fatalf("code = %v, want %s", got, codeInvalidArgument)
	}
	var stillThere struct {
		Session struct{ ID string }
	}
	mustQuery(t, ts, `{ session(id: "admin-bad-1") { id } }`, nil, &stillThere)
	if stillThere.Session.ID != "admin-bad-1" {
		t.Fatal("rejected batch deleted an id")
	}
}
