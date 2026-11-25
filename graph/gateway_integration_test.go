package graph

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	cityconfig "github.com/wricardo/drbrain/games/delivery/game/config"
	"github.com/wricardo/drbrain/games/delivery/game/session"
	deliverygraph "github.com/wricardo/drbrain/games/delivery/graph"
	mazesgraph "github.com/wricardo/drbrain/games/mazes/graph"
	motorgraph "github.com/wricardo/drbrain/games/motor/graph"
	teslagraph "github.com/wricardo/drbrain/games/tesla/graph"
	"github.com/wricardo/drbrain/platform/account"
	"github.com/wricardo/drbrain/platform/auth"
	"github.com/wricardo/drbrain/platform/pgdb/pgtest"
)

// TestDeliveryCitiesThroughUnifiedGateway exercises the generated adapter for a
// newly added field: the request must reach the per-game resolver (no
// "not implemented" panic) and list both shipped cities.
func TestDeliveryCitiesThroughUnifiedGateway(t *testing.T) {
	cat, err := cityconfig.Open(cityconfig.Options{
		BuiltinDir:    "../games/delivery/maps",
		DefaultCityID: "city",
	})
	if err != nil {
		t.Fatalf("open city catalogue: %v", err)
	}
	d := &deliverygraph.Resolver{Catalog: cat}
	h := NewHandler(Options{Resolver: New(nil, d, nil, nil, nil, nil)})

	out := postJSON(t, h, `{"query":"{ delivery { cities { id name status } } }"}`)
	if errs, ok := out["errors"]; ok {
		t.Fatalf("cities errors = %v", errs)
	}
	cities := out["data"].(map[string]any)["delivery"].(map[string]any)["cities"].([]any)
	ids := make(map[string]bool, len(cities))
	for _, raw := range cities {
		c := raw.(map[string]any)
		ids[c["id"].(string)] = true
		if c["status"] != "PUBLISHED" {
			t.Errorf("city %v status = %v, want PUBLISHED", c["id"], c["status"])
		}
	}
	for _, id := range []string{"city", "saopaulo"} {
		if !ids[id] {
			t.Fatalf("shipped city %q missing from %v", id, ids)
		}
	}
}

// TestDeliveryAdminThroughUnifiedGateway proves the admin mutation is wired
// through the generated adapter: a wrong key reaches the game resolver and is
// refused instead of panicking as "not implemented".
func TestDeliveryAdminThroughUnifiedGateway(t *testing.T) {
	d := &deliverygraph.Resolver{AdminKey: "sekret"}
	h := NewHandler(Options{Resolver: New(nil, d, nil, nil, nil, nil)})

	out := postJSON(t, h, `{"query":"mutation { deliveryAdmin(key: \"nope\") { deleteSession(id: \"x\") } }"}`)
	errs, ok := out["errors"].([]any)
	if !ok || len(errs) == 0 {
		t.Fatalf("wrong admin key accepted: %v", out)
	}
	msg, _ := errs[0].(map[string]any)["message"].(string)
	if !strings.Contains(msg, "admin key") {
		t.Fatalf("wrong-key message = %q", msg)
	}
}

// TestDeliveryAliasedMovesRunInOrder guards why delivery mutations are flat
// root fields: gqlgen resolves a nested object's fields concurrently, which
// scrambled a route queued as aliased moveCar calls inside delivery { }. Root
// mutation fields run serially, so the queue must equal the document order.
func TestDeliveryAliasedMovesRunInOrder(t *testing.T) {
	pool := pgtest.Pool(t)
	cat, err := cityconfig.Open(cityconfig.Options{
		BuiltinDir:    "../games/delivery/maps",
		DefaultCityID: "city",
		Pool:          pool,
	})
	if err != nil {
		t.Fatalf("open city catalogue: %v", err)
	}
	mgr, err := session.NewManager(cat, pool)
	if err != nil {
		t.Fatalf("manager: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Close() })
	d := &deliverygraph.Resolver{Mgr: mgr, Catalog: cat}
	h := NewHandler(Options{Resolver: New(nil, d, nil, nil, nil, nil)})

	created := postJSON(t, h, `{"query":"mutation { deliveryCreateSession(cityId: \"city\") { session { id } ownerToken } }"}`)
	res, ok := created["data"].(map[string]any)["deliveryCreateSession"].(map[string]any)
	if !ok {
		t.Fatalf("create session: %v", created)
	}
	id := res["session"].(map[string]any)["id"].(string)
	token := res["ownerToken"].(string)

	route := []string{"EAST", "EAST", "EAST", "SOUTH", "SOUTH", "SOUTH", "WEST", "WEST", "WEST"}
	var doc strings.Builder
	doc.WriteString("mutation {")
	for i, dir := range route {
		fmt.Fprintf(&doc, ` m%d: deliveryMoveCar(id: %q, carId: "car_1", direction: %s, ownerToken: %q) { id }`, i, id, dir, token)
	}
	doc.WriteString(" }")
	body, _ := json.Marshal(map[string]string{"query": doc.String()})
	if out := postJSON(t, h, string(body)); out["errors"] != nil {
		t.Fatalf("queue route: %v", out["errors"])
	}

	readBody, _ := json.Marshal(map[string]string{
		"query": fmt.Sprintf(`{ delivery { session(id: %q) { cars { queuedMoves } } } }`, id),
	})
	read := postJSON(t, h, string(readBody))
	data, _ := read["data"].(map[string]any)
	if data == nil {
		t.Fatalf("read session: %v", read)
	}
	cars := data["delivery"].(map[string]any)["session"].(map[string]any)["cars"].([]any)
	var got []string
	for _, m := range cars[0].(map[string]any)["queuedMoves"].([]any) {
		got = append(got, m.(string))
	}
	if strings.Join(got, ",") != strings.Join(route, ",") {
		t.Fatalf("queuedMoves = %v, want %v", got, route)
	}
}

// TestMazesAdminThroughUnifiedGateway proves the mazes admin namespace reaches
// the per-game resolver instead of panicking as "not implemented".
func TestMazesAdminThroughUnifiedGateway(t *testing.T) {
	t.Setenv("ADMIN_API_KEY", "sekret")
	h := NewHandler(Options{Resolver: New(nil, nil, &mazesgraph.Resolver{}, nil, nil, nil)})

	out := postJSON(t, h, `{"query":"mutation { mazes { admin(key: \"nope\") { deleteSession(id: \"x\") } } }"}`)
	assertAdminKeyError(t, out)
}

// TestMotorAdminThroughUnifiedGateway does the same for motorProgramming.
func TestMotorAdminThroughUnifiedGateway(t *testing.T) {
	m := &motorgraph.Resolver{AdminPolicy: motorgraph.Admin{Key: "sekret"}}
	h := NewHandler(Options{Resolver: New(nil, nil, nil, m, nil, nil)})

	out := postJSON(t, h, `{"query":"mutation { motorProgramming { admin(key: \"nope\") { pauseSession(id: \"x\") { id } } } }"}`)
	assertAdminKeyError(t, out)
}

func assertAdminKeyError(t *testing.T, out map[string]any) {
	t.Helper()
	errs, ok := out["errors"].([]any)
	if !ok || len(errs) == 0 {
		t.Fatalf("wrong admin key accepted: %v", out)
	}
	msg, _ := errs[0].(map[string]any)["message"].(string)
	if !strings.Contains(msg, "admin key") {
		t.Fatalf("wrong-key message = %q", msg)
	}
}

// fakeAccounts satisfies auth.Accounts for the viewer-context test.
type fakeAccounts struct {
	acct account.Account
}

func (f fakeAccounts) SignIn(string, string) (*account.Account, bool, error) {
	return &f.acct, false, nil
}
func (f fakeAccounts) Lookup(id string) (account.Account, bool) {
	return f.acct, id == f.acct.ID
}
func (f fakeAccounts) RegisterPassword(string, string) (*account.Account, error) {
	return &f.acct, nil
}
func (f fakeAccounts) VerifyPassword(string, string) (*account.Account, error) {
	return &f.acct, nil
}

// TestUnifiedHandlerPropagatesViewerAndRequest proves the middleware hands the
// signed-in viewer, the *http.Request (admin keys) and every per-game request
// key to the resolver context on a real HTTP request.
func TestUnifiedHandlerPropagatesViewerAndRequest(t *testing.T) {
	accounts := fakeAccounts{acct: account.Account{ID: "acct-123", Handle: "tester"}}
	authn, err := auth.New(auth.Config{
		Accounts:  accounts,
		Password:  true,
		JWTSecret: "test-secret-that-is-long-enough-for-hs256-signing",
	})
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := authn.IssueToken(accounts.acct.ID)
	if err != nil {
		t.Fatal(err)
	}

	var viewerID, requestKey string
	var gameKeys bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if v := auth.ViewerFrom(ctx); v != nil {
			viewerID = v.Account.ID
		}
		if req := auth.RequestFrom(ctx); req != nil {
			requestKey = req.Header.Get("X-Admin-Key")
		}
		gameKeys = ctx.Value(mazesgraph.HTTPRequestKey{}) != nil &&
			ctx.Value(motorgraph.HTTPRequestKey{}) != nil &&
			ctx.Value(teslagraph.HTTPRequestKey{}) != nil
	})
	req := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(`{"query":"{ __typename }"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Admin-Key", "sekret")
	withRequestContext(authn, next).ServeHTTP(httptest.NewRecorder(), req)

	if viewerID != accounts.acct.ID {
		t.Fatalf("viewer in context = %q, want %q", viewerID, accounts.acct.ID)
	}
	if requestKey != "sekret" {
		t.Fatalf("http request in context carried admin key %q", requestKey)
	}

	if !gameKeys {
		t.Fatal("per-game HTTPRequestKey values were not set")
	}
}
