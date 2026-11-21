package graph

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	cityconfig "github.com/wricardo/drbrain/games/delivery/game/config"
	deliverygraph "github.com/wricardo/drbrain/games/delivery/graph"
	mazesgraph "github.com/wricardo/drbrain/games/mazes/graph"
	motorgraph "github.com/wricardo/drbrain/games/motor/graph"
	teslagraph "github.com/wricardo/drbrain/games/tesla/graph"
	"github.com/wricardo/drbrain/platform/account"
	"github.com/wricardo/drbrain/platform/auth"
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
