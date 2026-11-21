package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/99designs/gqlgen/graphql/handler"

	"github.com/wricardo/drbrain/games/mazes/game/config"
	"github.com/wricardo/drbrain/games/mazes/game/engine"
	"github.com/wricardo/drbrain/games/mazes/game/service"
	"github.com/wricardo/drbrain/games/mazes/game/session"
	"github.com/wricardo/drbrain/games/mazes/graph/generated"
	"github.com/wricardo/drbrain/games/mazes/transport/websocket"
	"github.com/wricardo/drbrain/platform/account"
	"github.com/wricardo/drbrain/platform/auth"
	"github.com/wricardo/drbrain/platform/pgdb/pgtest"
)

// gqlResp decodes one GraphQL response envelope.
type gqlResp struct {
	Data   json.RawMessage `json:"data"`
	Errors []struct {
		Message    string         `json:"message"`
		Extensions map[string]any `json:"extensions"`
	} `json:"errors"`
}

func (r gqlResp) code() string {
	if len(r.Errors) == 0 {
		return ""
	}
	code, _ := r.Errors[0].Extensions["code"].(string)
	return code
}

// newPGMapServer builds a GraphQL server over a shipped "shipped" map and
// PostgreSQL player maps, returning its URL and the manager.
func newPGMapServer(t *testing.T, adminKey string) (string, *config.Manager, *Resolver) {
	t.Helper()
	t.Setenv("ADMIN_API_KEY", adminKey)
	t.Setenv("ALLOW_UNAUTHENTICATED_ADMIN", "")
	dir := t.TempDir()
	shipped := engine.MapConfig{
		ID: "shipped", Name: "Shipped",
		Levels: [][]string{{"SoF", "ooo", "ooo"}},
	}
	raw, err := json.MarshalIndent(shipped, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "shipped.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	mgr, err := config.NewManager(config.Options{Dir: dir, Pool: pgtest.Pool(t)})
	if err != nil {
		t.Fatalf("config.NewManager: %v", err)
	}
	sessions := session.NewManager(nil, 10)
	t.Cleanup(func() { _ = sessions })
	svc := service.New(mgr, sessions, nil, service.Options{})
	resolver := NewResolver(svc, websocket.NewHub())
	h := handler.NewDefaultServer(generated.NewExecutableSchema(generated.Config{Resolvers: resolver}))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), HTTPRequestKey{}, r)
		if acct := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "); acct != "" && acct != r.Header.Get("Authorization") {
			ctx = auth.WithViewer(ctx, &auth.Viewer{Account: &account.Account{ID: acct}})
		}
		h.ServeHTTP(w, r.WithContext(ctx))
	}))
	t.Cleanup(srv.Close)
	return srv.URL, mgr, resolver
}

// gqlAs posts an operation as a signed-in account ("" = guest).
func gqlAs(t *testing.T, url, accountID, adminKey, query string, variables map[string]any) gqlResp {
	t.Helper()
	body, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if accountID != "" {
		req.Header.Set("Authorization", "Bearer "+accountID)
	}
	if adminKey != "" {
		req.Header.Set("X-Admin-Key", adminKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	var out gqlResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return out
}

const createMapQ = `mutation($map: MapInput!) { createMap(map: $map) { id status ownedByMe } }`
const updateMapQ = `mutation($map: MapInput!) { updateMap(map: $map) { id status ownedByMe } }`
const publishMapQ = `mutation($id: ID!) { publishMap(id: $id) { id status ownedByMe } }`
const deleteMapQ = `mutation($id: ID!) { deleteMap(id: $id) }`
const validateMapQ = `mutation($map: MapInput!) { validateMap(map: $map) { valid finishReachable } }`
const mapsQ = `{ maps { id name status ownedByMe } }`
const mapQ = `query($id: ID!) { map(id: $id) { id status ownedByMe } }`

func mapVars(id string, levels [][]string) map[string]any {
	return map[string]any{"map": map[string]any{"id": id, "name": id, "levels": levels}}
}

func solvableLevels() [][]string { return [][]string{{"SoF", "ooo", "ooo"}} }
func trappedLevels() [][]string  { return [][]string{{"So.", "o..", "..F"}} }

func TestMazesMapMutationsRequireSignIn(t *testing.T) {
	url, _, _ := newPGMapServer(t, "")

	cases := []struct {
		name string
		q    string
		vars map[string]any
	}{
		{"createMap", createMapQ, mapVars("new", solvableLevels())},
		{"updateMap", updateMapQ, mapVars("shipped", solvableLevels())},
		{"deleteMap", deleteMapQ, map[string]any{"id": "shipped"}},
		{"publishMap", publishMapQ, map[string]any{"id": "shipped"}},
		{"validateMap", validateMapQ, mapVars("new", solvableLevels())},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := gqlAs(t, url, "", "", tc.q, tc.vars).code(); got != "UNAUTHENTICATED" {
				t.Fatalf("guest %s = %q, want UNAUTHENTICATED", tc.name, got)
			}
		})
	}
}

func TestMazesDraftLifecycleAndVisibility(t *testing.T) {
	url, _, _ := newPGMapServer(t, "")

	// A structurally valid draft whose finish is unreachable is accepted.
	created := gqlAs(t, url, "acct-a", "", createMapQ, mapVars("mine", trappedLevels()))
	if len(created.Errors) > 0 {
		t.Fatalf("createMap: %v", created.Errors)
	}
	var createOut struct {
		CreateMap struct {
			ID        string `json:"id"`
			Status    string `json:"status"`
			OwnedByMe bool   `json:"ownedByMe"`
		} `json:"createMap"`
	}
	decodeLifecycleData(t, created.Data, &createOut)
	if createOut.CreateMap.Status != "DRAFT" || !createOut.CreateMap.OwnedByMe {
		t.Fatalf("createMap = %+v", createOut.CreateMap)
	}

	// The owner sees the draft; another player and guests do not.
	if listed := gqlAs(t, url, "acct-a", "", mapsQ, nil); !mazesHasMap(listed, "mine") {
		t.Fatalf("owner's listing missing the draft: %v", listed.Errors)
	}
	if listed := gqlAs(t, url, "acct-b", "", mapsQ, nil); mazesHasMap(listed, "mine") {
		t.Fatal("another player must not list the draft")
	}
	if got := gqlAs(t, url, "acct-b", "", mapQ, map[string]any{"id": "mine"}); len(got.Errors) > 0 || !isEmptyData(got.Data, "map") {
		t.Fatalf("other player's map detail = %s %v, want null", got.Data, got.Errors)
	}
	if got := gqlAs(t, url, "", "", mapQ, map[string]any{"id": "mine"}); len(got.Errors) > 0 || !isEmptyData(got.Data, "map") {
		t.Fatalf("guest map detail = %s %v, want null", got.Data, got.Errors)
	}

	// Only the owner may start a session on the draft.
	if got := gqlAs(t, url, "acct-b", "", `mutation { createSession(mapID: "mine") { session { id } } }`, nil); got.code() != service.CodeNotFound {
		t.Fatalf("other player's createSession = %v, want NOT_FOUND", got.Errors)
	}
	if got := gqlAs(t, url, "acct-a", "", `mutation { createSession(mapID: "mine") { session { id } } }`, nil); len(got.Errors) > 0 {
		t.Fatalf("owner's createSession: %v", got.Errors)
	}

	// Another player cannot edit, publish or delete it.
	if got := gqlAs(t, url, "acct-b", "", updateMapQ, mapVars("mine", solvableLevels())); got.code() != service.CodeForbidden {
		t.Fatalf("other player's update = %v, want FORBIDDEN", got.Errors)
	}
	if got := gqlAs(t, url, "acct-b", "", publishMapQ, map[string]any{"id": "mine"}); got.code() != service.CodeForbidden {
		t.Fatalf("other player's publish = %v, want FORBIDDEN", got.Errors)
	}
	if got := gqlAs(t, url, "acct-b", "", deleteMapQ, map[string]any{"id": "mine"}); got.code() != service.CodeForbidden {
		t.Fatalf("other player's delete = %v, want FORBIDDEN", got.Errors)
	}

	// A save stays a draft; publishing still requires a reachable finish.
	if got := gqlAs(t, url, "acct-a", "", updateMapQ, mapVars("mine", solvableLevels())); len(got.Errors) > 0 || !bytes.Contains(got.Data, []byte(`"status":"DRAFT"`)) {
		t.Fatalf("owner update = %s %v, want DRAFT", got.Data, got.Errors)
	}
	if got := gqlAs(t, url, "acct-a", "", publishMapQ, map[string]any{"id": "mine"}); len(got.Errors) > 0 || !bytes.Contains(got.Data, []byte(`"status":"PUBLISHED"`)) {
		t.Fatalf("publish = %s %v, want PUBLISHED", got.Data, got.Errors)
	}

	// The published map is visible and playable by everyone.
	if listed := gqlAs(t, url, "acct-b", "", mapsQ, nil); !mazesHasMap(listed, "mine") {
		t.Fatal("published map missing from another player's listing")
	}
	if got := gqlAs(t, url, "acct-b", "", `mutation { createSession(mapID: "mine") { session { id } } }`, nil); len(got.Errors) > 0 {
		t.Fatalf("createSession on a published map: %v", got.Errors)
	}

	// The owner deletes it.
	if got := gqlAs(t, url, "acct-a", "", deleteMapQ, map[string]any{"id": "mine"}); len(got.Errors) > 0 {
		t.Fatalf("owner delete: %v", got.Errors)
	}
}

func TestMazesPublishRefusesUnreachableFinish(t *testing.T) {
	url, _, _ := newPGMapServer(t, "")
	if got := gqlAs(t, url, "acct-a", "", createMapQ, mapVars("trapped", trappedLevels())); len(got.Errors) > 0 {
		t.Fatalf("createMap: %v", got.Errors)
	}
	if got := gqlAs(t, url, "acct-a", "", publishMapQ, map[string]any{"id": "trapped"}); got.code() != service.CodeInvalidArgument {
		t.Fatalf("publish unreachable = %v, want INVALID_ARGUMENT", got.Errors)
	}

	// validateMap reports the same gate: valid but finish unreachable.
	got := gqlAs(t, url, "acct-a", "", validateMapQ, mapVars("trapped", trappedLevels()))
	if len(got.Errors) > 0 {
		t.Fatalf("validateMap: %v", got.Errors)
	}
	var out struct {
		ValidateMap struct {
			FinishReachable bool `json:"finishReachable"`
		} `json:"validateMap"`
	}
	decodeLifecycleData(t, got.Data, &out)
	if out.ValidateMap.FinishReachable {
		t.Fatal("unreachable finish reported as reachable")
	}
}

func TestMazesShippedMapAdminGate(t *testing.T) {
	url, _, _ := newPGMapServer(t, "secret")

	// Players cannot edit a shipped map, even signed in.
	if got := gqlAs(t, url, "acct-a", "", updateMapQ, mapVars("shipped", solvableLevels())); got.code() != service.CodeForbidden {
		t.Fatalf("player edit of a shipped map = %v, want FORBIDDEN", got.Errors)
	}
	// AdminMap remains admin-key only.
	if got := gqlAs(t, url, "acct-a", "", `{ adminMap(id: "shipped") { id } }`, nil); got.code() != service.CodeForbidden {
		t.Fatalf("adminMap without key = %v, want FORBIDDEN", got.Errors)
	}
	if got := gqlAs(t, url, "acct-a", "secret", `{ adminMap(id: "shipped") { id } }`, nil); len(got.Errors) > 0 {
		t.Fatalf("adminMap with key: %v", got.Errors)
	}
	// The admin key overrides ownership for shipped maps.
	if got := gqlAs(t, url, "acct-a", "secret", updateMapQ, mapVars("shipped", solvableLevels())); len(got.Errors) > 0 {
		t.Fatalf("admin edit of a shipped map: %v", got.Errors)
	}
}

func mazesHasMap(resp gqlResp, id string) bool {
	if len(resp.Data) == 0 {
		return false
	}
	var out struct {
		Maps []struct {
			ID string `json:"id"`
		} `json:"maps"`
	}
	if err := json.Unmarshal(resp.Data, &out); err != nil {
		return false
	}
	for _, m := range out.Maps {
		if m.ID == id {
			return true
		}
	}
	return false
}

// isEmptyData reports whether the single field in raw data is null.
func isEmptyData(raw json.RawMessage, field string) bool {
	var out map[string]json.RawMessage
	if err := json.Unmarshal(raw, &out); err != nil {
		return false
	}
	return string(bytes.TrimSpace(out[field])) == "null"
}

// decodeLifecycleData re-decodes a raw GraphQL data payload into out.
func decodeLifecycleData(t *testing.T, raw json.RawMessage, out any) {
	t.Helper()
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatalf("decode gql data: %v", err)
	}
}

// adminMap returns the full editable layout to the map's signed-in owner; it
// stays admin-key only for shipped maps, and hides other players' maps.
func TestAdminMapOwnerAccess(t *testing.T) {
	url, _, _ := newPGMapServer(t, "")

	created := gqlAs(t, url, "acct-a", "", createMapQ, mapVars("mine", solvableLevels()))
	if len(created.Errors) > 0 {
		t.Fatalf("createMap: %v", created.Errors)
	}

	query := `query($id: ID!) { adminMap(id: $id) { id status ownedByMe levels } }`

	// The owner can reload the full layout of their own draft.
	owned := gqlAs(t, url, "acct-a", "", query, map[string]any{"id": "mine"})
	if len(owned.Errors) > 0 {
		t.Fatalf("owner adminMap: %v", owned.Errors)
	}
	var out struct {
		AdminMap *struct {
			ID        string     `json:"id"`
			Status    string     `json:"status"`
			OwnedByMe bool       `json:"ownedByMe"`
			Levels    [][]string `json:"levels"`
		} `json:"adminMap"`
	}
	decodeLifecycleData(t, owned.Data, &out)
	if out.AdminMap == nil || out.AdminMap.ID != "mine" || out.AdminMap.Status != "DRAFT" ||
		!out.AdminMap.OwnedByMe || len(out.AdminMap.Levels) != 1 || len(out.AdminMap.Levels[0]) != 3 {
		t.Fatalf("owner adminMap = %+v", out.AdminMap)
	}

	// Another player and guests get null, not an error that confirms the map.
	if got := gqlAs(t, url, "acct-b", "", query, map[string]any{"id": "mine"}); len(got.Errors) > 0 || !isEmptyData(got.Data, "adminMap") {
		t.Fatalf("other player's adminMap = %s %v, want null", got.Data, got.Errors)
	}
	if got := gqlAs(t, url, "", "", query, map[string]any{"id": "mine"}); len(got.Errors) > 0 || !isEmptyData(got.Data, "adminMap") {
		t.Fatalf("guest adminMap = %s %v, want null", got.Data, got.Errors)
	}

	// The owner keeps access after publishing.
	if got := gqlAs(t, url, "acct-a", "", publishMapQ, map[string]any{"id": "mine"}); len(got.Errors) > 0 {
		t.Fatalf("publish: %v", got.Errors)
	}
	published := gqlAs(t, url, "acct-a", "", query, map[string]any{"id": "mine"})
	if len(published.Errors) > 0 || !bytes.Contains(published.Data, []byte(`"status":"PUBLISHED"`)) {
		t.Fatalf("owner adminMap after publish = %s %v", published.Data, published.Errors)
	}
}

// sessionVisFake serves one catalogue entry to the session-visibility helpers.
type sessionVisFake struct {
	service.GameService
	entry    *service.MapEntry
	entryErr error
	sess     *service.Session
}

func (f sessionVisFake) GetSession(string) (*service.Session, error) { return f.sess, nil }
func (f sessionVisFake) GetMapEntry(string) (*service.MapEntry, error) {
	if f.entryErr != nil {
		return nil, f.entryErr
	}
	return f.entry, nil
}

func TestDraftSessionVisibilityHelpers(t *testing.T) {
	sess := &service.Session{ID: "s", MapID: "m", OwnerAccountID: "acct-a"}
	ownerless := &service.MapEntry{ID: "m", Status: service.MapStatusDraft}
	r := &Resolver{Service: sessionVisFake{entry: ownerless, sess: sess}}

	if r.sessionVisible(viewerCtx(""), sess) {
		t.Fatal("guest must not see a session on an ownerless draft")
	}
	if r.sessionVisible(viewerCtx("acct-a"), sess) {
		t.Fatal("a player must not own an ownerless draft")
	}

	owned := &service.MapEntry{ID: "m", Status: service.MapStatusDraft, OwnerID: "acct-a"}
	r = &Resolver{Service: sessionVisFake{entry: owned, sess: sess}}
	if !r.sessionVisible(viewerCtx("acct-a"), sess) {
		t.Fatal("the owner must see their draft session")
	}

	// Deleted map: only the non-empty session owner may read it.
	r = &Resolver{Service: sessionVisFake{entryErr: service.Errorf(service.CodeNotFound, "gone"), sess: sess}}
	if !r.sessionVisible(viewerCtx("acct-a"), sess) {
		t.Fatal("the session owner must see a session whose map was deleted")
	}
	if r.sessionVisible(viewerCtx("acct-b"), sess) || r.sessionVisible(viewerCtx(""), sess) {
		t.Fatal("another player or guest must not see a deleted-map session")
	}
	legacy := &service.Session{ID: "old", MapID: "gone"}
	if r.sessionVisible(viewerCtx("acct-a"), legacy) {
		t.Fatal("a legacy session without an owner must stay hidden after map deletion")
	}
}

func TestDraftSessionVisibilityOverHTTP(t *testing.T) {
	url, _, res := newPGMapServer(t, "")

	if got := gqlAs(t, url, "acct-a", "", createMapQ, mapVars("mine", solvableLevels())); len(got.Errors) > 0 {
		t.Fatalf("createMap: %v", got.Errors)
	}
	created := gqlAs(t, url, "acct-a", "", `mutation { createSession(mapID: "mine") { session { id } } }`, nil)
	if len(created.Errors) > 0 {
		t.Fatalf("createSession: %v", created.Errors)
	}
	var sessOut struct {
		CreateSession struct {
			Session struct {
				ID string `json:"id"`
			} `json:"session"`
		} `json:"createSession"`
	}
	decodeLifecycleData(t, created.Data, &sessOut)
	id := sessOut.CreateSession.Session.ID
	if id == "" {
		t.Fatal("no session id")
	}

	// The owner reads, lists and can play the session.
	if got := gqlAs(t, url, "acct-a", "", `query($id: ID!) { session(id: $id) { id } }`, map[string]any{"id": id}); len(got.Errors) > 0 {
		t.Fatalf("owner session: %v", got.Errors)
	}
	if got := gqlAs(t, url, "acct-a", "", `{ sessions { id } }`, nil); !mazesHasSession(got, id) {
		t.Fatalf("owner list missing draft session: %s", got.Data)
	}
	if got := gqlAs(t, url, "acct-a", "", `query($id: ID!) { canPlaySession(id: $id) }`, map[string]any{"id": id}); !boolField(got, "canPlaySession") {
		t.Fatalf("owner canPlaySession = %s", got.Data)
	}

	// Another player and guests get null/false/no row.
	for _, acct := range []string{"acct-b", ""} {
		if got := gqlAs(t, url, acct, "", `query($id: ID!) { session(id: $id) { id } }`, map[string]any{"id": id}); len(got.Errors) > 0 || !isEmptyData(got.Data, "session") {
			t.Fatalf("%q session = %s %v, want null", acct, got.Data, got.Errors)
		}
		if got := gqlAs(t, url, acct, "", `{ sessions { id } }`, nil); mazesHasSession(got, id) {
			t.Fatalf("%q sees a draft session in the list", acct)
		}
		if got := gqlAs(t, url, acct, "", `query($id: ID!) { canPlaySession(id: $id) }`, map[string]any{"id": id}); boolField(got, "canPlaySession") {
			t.Fatalf("%q can play a draft session", acct)
		}
	}

	// The subscription refuses hidden sessions.
	sub := res.Subscription()
	if _, err := sub.SessionUpdated(viewerCtx("acct-b"), id); err == nil {
		t.Fatal("subscription to another player's draft session must fail")
	}
	ownerCtx, cancel := context.WithCancel(viewerCtx("acct-a"))
	defer cancel()
	if _, err := sub.SessionUpdated(ownerCtx, id); err != nil {
		t.Fatalf("owner subscription: %v", err)
	}

	// Publishing makes the session public.
	if got := gqlAs(t, url, "acct-a", "", publishMapQ, map[string]any{"id": "mine"}); len(got.Errors) > 0 {
		t.Fatalf("publishMap: %v", got.Errors)
	}
	if got := gqlAs(t, url, "acct-b", "", `query($id: ID!) { session(id: $id) { id } }`, map[string]any{"id": id}); len(got.Errors) > 0 || isEmptyData(got.Data, "session") {
		t.Fatalf("published session for another player = %s %v", got.Data, got.Errors)
	}
}

func mazesHasSession(resp gqlResp, id string) bool {
	var out struct {
		Sessions []struct {
			ID string `json:"id"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(resp.Data, &out); err != nil {
		return false
	}
	for _, s := range out.Sessions {
		if s.ID == id {
			return true
		}
	}
	return false
}

func boolField(resp gqlResp, field string) bool {
	var out map[string]any
	if err := json.Unmarshal(resp.Data, &out); err != nil {
		return false
	}
	v, _ := out[field].(bool)
	return v
}

// A session outlives its map; after deletion only its creator may read it.
func TestDeletedMapSessionVisibility(t *testing.T) {
	url, _, _ := newPGMapServer(t, "")

	start := func(mapID string, publish bool) string {
		t.Helper()
		if got := gqlAs(t, url, "acct-a", "", createMapQ, mapVars(mapID, solvableLevels())); len(got.Errors) > 0 {
			t.Fatalf("createMap %s: %v", mapID, got.Errors)
		}
		if publish {
			if got := gqlAs(t, url, "acct-a", "", publishMapQ, map[string]any{"id": mapID}); len(got.Errors) > 0 {
				t.Fatalf("publishMap %s: %v", mapID, got.Errors)
			}
		}
		created := gqlAs(t, url, "acct-a", "", `mutation($id: ID!) { createSession(mapID: $id) { session { id } } }`, map[string]any{"id": mapID})
		if len(created.Errors) > 0 {
			t.Fatalf("createSession %s: %v", mapID, created.Errors)
		}
		var out struct {
			CreateSession struct {
				Session struct {
					ID string `json:"id"`
				} `json:"session"`
			} `json:"createSession"`
		}
		decodeLifecycleData(t, created.Data, &out)
		if out.CreateSession.Session.ID == "" {
			t.Fatalf("no session id for %s", mapID)
		}
		if got := gqlAs(t, url, "acct-a", "", deleteMapQ, map[string]any{"id": mapID}); len(got.Errors) > 0 {
			t.Fatalf("deleteMap %s: %v", mapID, got.Errors)
		}
		return out.CreateSession.Session.ID
	}

	for _, tc := range []struct {
		name    string
		publish bool
	}{
		{"draft deleted", false},
		{"published deleted", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := start("gone", tc.publish)
			query := `query($id: ID!) { session(id: $id) { id } }`
			if got := gqlAs(t, url, "acct-a", "", query, map[string]any{"id": id}); len(got.Errors) > 0 || isEmptyData(got.Data, "session") {
				t.Fatalf("creator session after deletion = %s %v", got.Data, got.Errors)
			}
			for _, acct := range []string{"acct-b", ""} {
				if got := gqlAs(t, url, acct, "", query, map[string]any{"id": id}); len(got.Errors) > 0 || !isEmptyData(got.Data, "session") {
					t.Fatalf("%q session after deletion = %s %v, want null", acct, got.Data, got.Errors)
				}
			}
		})
	}
}
