package graph

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/wricardo/drbrain/games/delivery/game/session"
	"github.com/wricardo/drbrain/games/delivery/graph/generated"
	"github.com/wricardo/drbrain/platform/account"
	"github.com/wricardo/drbrain/platform/auth"
)

// newOwnershipServer is a GraphQL server with the account service wired in. A
// request carries the signed-in viewer as X-Test-Account: <id>, which the test
// middleware turns into the same auth.Viewer the platform handler installs.
func newOwnershipServer(t *testing.T) (*httptest.Server, *session.Manager, *account.Service, string, string) {
	t.Helper()
	city, err := filepath.Abs(filepath.Join("..", "maps", "city.json"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	mgr, err := session.NewManager(city, dir)
	if err != nil {
		t.Fatalf("manager: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Close() })

	repo, err := account.Open(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("accounts: %v", err)
	}
	accounts := account.New(account.Options{Store: repo})
	acct, _, err := accounts.SignIn("password", "alice")
	if err != nil {
		t.Fatalf("sign in: %v", err)
	}

	srv := handler.New(generated.NewExecutableSchema(generated.Config{
		Resolvers: &Resolver{Mgr: mgr, Accounts: accounts},
	}))
	// SSE first: it must claim its Accept header before the plain POST transport.
	srv.AddTransport(transport.SSE{})
	srv.AddTransport(transport.POST{})
	srv.AddTransport(transport.GET{})
	srv.AddTransport(transport.Options{})

	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id := r.Header.Get("X-Test-Account"); id != "" {
			if a, ok := accounts.Lookup(id); ok {
				r = r.WithContext(auth.WithViewer(r.Context(), &auth.Viewer{Account: &a}))
			}
		}
		srv.ServeHTTP(w, r)
	})
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return ts, mgr, accounts, acct.ID, dir
}

// gqlAs posts one operation, optionally as a signed-in account.
func gqlAs(t *testing.T, ts *httptest.Server, accountID, q string, vars map[string]any) gqlResponse {
	t.Helper()
	body, err := json.Marshal(map[string]any{"query": q, "variables": vars})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, ts.URL, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if accountID != "" {
		req.Header.Set("X-Test-Account", accountID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out gqlResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

// createForToken creates a session and returns its id and owner token.
func createForToken(t *testing.T, ts *httptest.Server, accountID, id string) (string, string) {
	t.Helper()
	var out struct {
		CreateSession struct {
			Session    struct{ ID string }
			OwnerToken string `json:"ownerToken"`
		}
	}
	res := gqlAs(t, ts, accountID, `mutation ($id: ID) {
		createSession(id: $id, seed: 3) { session { id } ownerToken }
	}`, map[string]any{"id": id})
	if len(res.Errors) > 0 {
		t.Fatalf("create: %v", res.Errors)
	}
	if err := json.Unmarshal(res.Data, &out); err != nil {
		t.Fatal(err)
	}
	if out.CreateSession.OwnerToken == "" {
		t.Fatal("createSession returned no owner token")
	}
	return out.CreateSession.Session.ID, out.CreateSession.OwnerToken
}

func errText(res gqlResponse) string {
	if len(res.Errors) == 0 {
		return ""
	}
	return res.Errors[0].Message
}

func TestCreateSessionReturnsTokenOnlyThere(t *testing.T) {
	ts, _, _, _, _ := newOwnershipServer(t)
	id, token := createForToken(t, ts, "", "tok1")
	if len(token) != 43 {
		t.Fatalf("token = %q, want 43 base64url chars", token)
	}

	// Reads stay public and never carry the token.
	reads := []string{
		`query ($id: ID!) { session(id: $id) { id tick } }`,
		`{ sessions { id ownerDisplayName } }`,
	}
	for _, q := range reads {
		res := gqlAs(t, ts, "", q, map[string]any{"id": id})
		if len(res.Errors) > 0 {
			t.Fatalf("public read failed: %v", res.Errors)
		}
		if bytes.Contains(res.Data, []byte(token)) {
			t.Fatalf("a read leaked the owner token: %s", res.Data)
		}
	}
}

func TestGatedMutationsRejectMissingAndWrongTokens(t *testing.T) {
	ts, _, _, _, _ := newOwnershipServer(t)
	id, _ := createForToken(t, ts, "", "gate1")

	cases := []struct {
		name  string
		query string
	}{
		{"moveCar", `mutation ($id: ID!, $t: String) { moveCar(id: $id, carId: "car_1", direction: SOUTH, ownerToken: $t) { id } }`},
		{"tick", `mutation ($id: ID!, $t: String) { tick(id: $id, count: 1, ownerToken: $t) { id } }`},
		{"setSpeed", `mutation ($id: ID!, $t: String) { setSpeed(id: $id, speed: 1, ownerToken: $t) { id } }`},
		{"assignOrder", `mutation ($id: ID!, $t: String) { assignOrder(id: $id, orderId: "o1", carId: "car_1", ownerToken: $t) { id } }`},
		{"unassignOrder", `mutation ($id: ID!, $t: String) { unassignOrder(id: $id, orderId: "o1", ownerToken: $t) { id } }`},
		{"buyCar", `mutation ($id: ID!, $t: String) { buyCar(id: $id, className: "van", ownerToken: $t) { id } }`},
		{"towCar", `mutation ($id: ID!, $t: String) { towCar(id: $id, carId: "car_1", ownerToken: $t) { id } }`},
		{"build", `mutation ($id: ID!, $t: String) { build(id: $id, buildId: "b01_corner_charger", ownerToken: $t) { id } }`},
		{"saveSession", `mutation ($id: ID!, $t: String) { saveSession(id: $id, ownerToken: $t) { id } }`},
		{"evictSession", `mutation ($id: ID!, $t: String) { evictSession(id: $id, ownerToken: $t) }`},
		{"deleteSession", `mutation ($id: ID!, $t: String) { deleteSession(id: $id, ownerToken: $t) }`},
	}
	for _, tc := range cases {
		for _, vars := range []map[string]any{
			{"id": id},
			{"id": id, "t": "not-the-token"},
		} {
			msg := errText(gqlAs(t, ts, "", tc.query, vars))
			if !strings.HasPrefix(msg, "forbidden:") {
				t.Errorf("%s (%v): message = %q, want a forbidden: prefix", tc.name, vars, msg)
			}
		}
	}

	// The save is still there and still owned.
	res := gqlAs(t, ts, "", `query ($id: ID!) { session(id: $id) { id } }`, map[string]any{"id": id})
	if len(res.Errors) > 0 {
		t.Fatalf("the session disappeared: %v", res.Errors)
	}
}

func TestUnknownSessionIsNotFoundNotForbidden(t *testing.T) {
	ts, _, _, _, _ := newOwnershipServer(t)
	res := gqlAs(t, ts, "", `mutation { tick(id: "0000000000000000", count: 1) { id } }`, nil)
	if msg := errText(res); !strings.HasPrefix(msg, "not_found:") {
		t.Fatalf("message = %q, want not_found:", msg)
	}
	// An invalid id is bad_request, and delete reports it with its prefix.
	res = gqlAs(t, ts, "", `mutation { deleteSession(id: "../etc") }`, nil)
	if msg := errText(res); !strings.HasPrefix(msg, "bad_request:") {
		t.Fatalf("message = %q, want bad_request:", msg)
	}
}

func TestSignedInOwnerNeedsNoToken(t *testing.T) {
	ts, _, accounts, alice, _ := newOwnershipServer(t)
	id, _ := createForToken(t, ts, alice, "mine1")

	res := gqlAs(t, ts, alice, `mutation ($id: ID!) { tick(id: $id, count: 1) { tick } }`, map[string]any{"id": id})
	if len(res.Errors) > 0 {
		t.Fatalf("the owning account was refused: %v", res.Errors)
	}
	// A different signed-in player is still a stranger.
	bob, _, err := accounts.SignIn("password", "bob")
	if err != nil {
		t.Fatal(err)
	}
	res = gqlAs(t, ts, bob.ID, `mutation ($id: ID!) { tick(id: $id, count: 1) { tick } }`, map[string]any{"id": id})
	if msg := errText(res); !strings.HasPrefix(msg, "forbidden:") {
		t.Fatalf("another account: message = %q, want forbidden:", msg)
	}
}

func TestLegacySaveStaysOpen(t *testing.T) {
	ts, mgr, _, _, dir := newOwnershipServer(t)
	id, _ := createForToken(t, ts, "", "legacy1")
	if err := mgr.SaveAll(); err != nil {
		t.Fatal(err)
	}

	// Rewrite the save into the pre-owner-token shape.
	path := filepath.Join(dir, id+".json")
	blob, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(blob, &doc); err != nil {
		t.Fatal(err)
	}
	delete(doc, "account_id")
	delete(doc, "owner_hash")
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
	mgr.Evict(id)

	res := gqlAs(t, ts, "", `mutation ($id: ID!) { tick(id: $id, count: 1) { tick } }`, map[string]any{"id": id})
	if len(res.Errors) > 0 {
		t.Fatalf("a legacy save was locked: %v", res.Errors)
	}
}

func TestClaimSessionsRequiresSignInAndAttaches(t *testing.T) {
	ts, _, _, alice, _ := newOwnershipServer(t)
	id, token := createForToken(t, ts, "", "claim1")

	res := gqlAs(t, ts, "", `mutation ($c: [SessionClaimInput!]!) { claimSessions(claims: $c) { sessionID status } }`,
		map[string]any{"c": []map[string]any{{"sessionID": id, "ownerToken": token}}})
	if msg := errText(res); !strings.HasPrefix(msg, "unauthenticated:") {
		t.Fatalf("message = %q, want unauthenticated:", msg)
	}

	var out struct {
		ClaimSessions []struct {
			SessionID string
			Status    string
		}
	}
	res = gqlAs(t, ts, alice, `mutation ($c: [SessionClaimInput!]!) { claimSessions(claims: $c) { sessionID status } }`,
		map[string]any{"c": []map[string]any{{"sessionID": id, "ownerToken": token}}})
	if len(res.Errors) > 0 {
		t.Fatalf("claim: %v", res.Errors)
	}
	if err := json.Unmarshal(res.Data, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.ClaimSessions) != 1 || out.ClaimSessions[0].Status != "ATTACHED" {
		t.Fatalf("claim = %+v, want one ATTACHED", out.ClaimSessions)
	}

	// The list row now attributes the save to alice and marks it mine.
	var list struct {
		Sessions []struct {
			OwnerDisplayName string
			OwnerHandle      *string
			Mine             bool
		}
	}
	res = gqlAs(t, ts, alice, `{ sessions { ownerDisplayName ownerHandle mine } }`, nil)
	if len(res.Errors) > 0 {
		t.Fatalf("list: %v", res.Errors)
	}
	if err := json.Unmarshal(res.Data, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Sessions) != 1 {
		t.Fatalf("sessions = %+v", list.Sessions)
	}
	row := list.Sessions[0]
	if !row.Mine || row.OwnerDisplayName == "Guest" || row.OwnerHandle == nil {
		t.Fatalf("row = %+v, want attributed to alice", row)
	}

	// Claiming again is idempotent from the player's point of view.
	res = gqlAs(t, ts, alice, `mutation ($c: [SessionClaimInput!]!) { claimSessions(claims: $c) { status } }`,
		map[string]any{"c": []map[string]any{{"sessionID": id, "ownerToken": token}}})
	if err := json.Unmarshal(res.Data, &out); err != nil {
		t.Fatal(err)
	}
	if out.ClaimSessions[0].Status != "ALREADY_YOURS" {
		t.Fatalf("second claim = %q", out.ClaimSessions[0].Status)
	}
}

func TestGuestSummaryIsGuest(t *testing.T) {
	ts, _, _, _, _ := newOwnershipServer(t)
	_, _ = createForToken(t, ts, "", "guest1")

	var out struct {
		Sessions []struct {
			OwnerDisplayName string
			OwnerHandle      *string
			Mine             bool
		}
	}
	res := gqlAs(t, ts, "", `{ sessions { ownerDisplayName ownerHandle mine } }`, nil)
	if err := json.Unmarshal(res.Data, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Sessions) != 1 {
		t.Fatalf("sessions = %+v", out.Sessions)
	}
	if out.Sessions[0].OwnerDisplayName != "Guest" || out.Sessions[0].OwnerHandle != nil || out.Sessions[0].Mine {
		t.Fatalf("guest row = %+v", out.Sessions[0])
	}
}

func TestCanPlaySession(t *testing.T) {
	ts, _, _, alice, _ := newOwnershipServer(t)
	id, token := createForToken(t, ts, "", "can1")

	cases := []struct {
		name    string
		account string
		vars    map[string]any
		want    bool
	}{
		{"no credential", "", map[string]any{"id": id}, false},
		{"wrong token", "", map[string]any{"id": id, "t": "nope"}, false},
		{"right token", "", map[string]any{"id": id, "t": token}, true},
		{"unknown session", "", map[string]any{"id": "0000000000000000"}, false},
		{"stranger account", alice, map[string]any{"id": id}, false},
	}
	for _, tc := range cases {
		var out struct{ CanPlaySession bool }
		res := gqlAs(t, ts, tc.account, `query ($id: ID!, $t: String) { canPlaySession(id: $id, ownerToken: $t) }`, tc.vars)
		if len(res.Errors) > 0 {
			t.Fatalf("%s: %v", tc.name, res.Errors)
		}
		if err := json.Unmarshal(res.Data, &out); err != nil {
			t.Fatal(err)
		}
		if out.CanPlaySession != tc.want {
			t.Errorf("%s: canPlaySession = %v, want %v", tc.name, out.CanPlaySession, tc.want)
		}
	}
}

// Reads stay public, including the live stream: a spectator may watch.
func TestSubscriptionIsPublic(t *testing.T) {
	ts, _, _, _, _ := newOwnershipServer(t)
	id, _ := createForToken(t, ts, "", "sub1")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	body, err := json.Marshal(map[string]any{
		"query":     `subscription ($id: ID!) { session(id: $id) { id tick } }`,
		"variables": map[string]any{"id": id},
	})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ts.URL, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		var b bytes.Buffer
		_, _ = b.ReadFrom(resp.Body)
		t.Fatalf("content type %q, body %s", ct, b.String())
	}
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		if strings.HasPrefix(sc.Text(), "data:") {
			if !strings.Contains(sc.Text(), id) {
				t.Fatalf("first frame does not describe %s: %s", id, sc.Text())
			}
			return
		}
	}
	t.Fatalf("no data frame: %v", sc.Err())
}
