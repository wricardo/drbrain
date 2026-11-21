package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/wricardo/drbrain/games/tesla/appconfig"
	"github.com/wricardo/drbrain/games/tesla/game/config"
	"github.com/wricardo/drbrain/games/tesla/game/engine"
	"github.com/wricardo/drbrain/games/tesla/game/service"
	"github.com/wricardo/drbrain/games/tesla/game/session"
	"github.com/wricardo/drbrain/games/tesla/graph/generated"
	"github.com/wricardo/drbrain/games/tesla/transport/websocket"
	"github.com/wricardo/drbrain/platform/account"
	"github.com/wricardo/drbrain/platform/auth"
	"github.com/wricardo/drbrain/platform/pgdb/pgtest"

	"github.com/99designs/gqlgen/graphql/handler"
	"net/http/httptest"
	"path/filepath"

	"os"
)

// newPGMapServer builds a GraphQL server whose shipped maps are files in a temp
// dir and whose player maps live in PostgreSQL. It returns the server URL and
// the config manager so tests can seed legacy ownerless rows.
func newPGMapServer(t *testing.T, opts ...Option) (string, *config.Manager, *Resolver) {
	t.Helper()
	root := t.TempDir()
	builtin := filepath.Join(root, "builtin")
	if err := os.MkdirAll(builtin, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	copyMapFile(t, "../maps/classic.json", filepath.Join(builtin, "classic.json"))

	mgr, err := config.NewManager(config.Options{ConfigDir: builtin, Pool: pgtest.Pool(t)})
	if err != nil {
		t.Fatalf("config.NewManager: %v", err)
	}
	svc := service.NewGameService(session.NewManager(), mgr)
	resolver := NewResolver(svc, nil, opts...)
	h := handler.NewDefaultServer(generated.NewExecutableSchema(generated.Config{Resolvers: resolver}))
	srv := httptest.NewServer(withRequestContext(h))
	t.Cleanup(srv.Close)
	return srv.URL, mgr, resolver
}

func copyMapFile(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read %s: %v", src, err)
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", dst, err)
	}
}

const createMapMutation = `mutation($name: String!, $map: GameMapInput!) {
  createMap(name: $name, map: $map) { name builtin status ownedByMe editKey }
}`

const updateMapMutation = `mutation($name: String!, $patch: GameMapPatchInput!, $editKey: String) {
  updateMap(name: $name, patch: $patch, editKey: $editKey) { name description builtin status ownedByMe }
}`

const deleteMapMutation = `mutation($name: String!, $editKey: String) {
  deleteMap(name: $name, editKey: $editKey) { message }
}`

const publishMapMutation = `mutation($name: String!) {
  publishMap(name: $name) { name status ownedByMe }
}`

const claimMapMutation = `mutation($name: String!, $editKey: String!) {
  claimMap(name: $name, editKey: $editKey) { name status ownedByMe editKey }
}`

const mapsQuery = `{ maps { mapId name builtin status ownedByMe } }`

const mapQuery = `query($name: String!) { map(name: $name) { name builtin status ownedByMe } }`

func mapInput(name string) map[string]any {
	return map[string]any{
		"name":            name,
		"description":     "test map",
		"gridSize":        5,
		"maxBattery":      10,
		"startingBattery": 10,
		"layout":          []string{"BBBBB", "BRHPB", "BRRSB", "BPPPB", "BBBBB"},
		"legend":          []any{map[string]any{"key": "R", "value": "road"}, map[string]any{"key": "H", "value": "home"}, map[string]any{"key": "P", "value": "park"}, map[string]any{"key": "S", "value": "supercharger"}, map[string]any{"key": "W", "value": "water"}, map[string]any{"key": "B", "value": "building"}},
		"cellConfigs":     []any{},
	}
}

// unwinnableMapInput is structurally valid but the park is walled in, so the
// publication proof fails while a draft may still be stored.
func unwinnableMapInput(name string) map[string]any {
	in := mapInput(name)
	in["layout"] = []string{"BBBBB", "BHBBB", "BBPBB", "BBBBB", "BBBBB"}
	return in
}

// gqlAs posts a GraphQL operation as the given signed-in account ("" =
// guest). adminKey, when non-empty, is sent as X-Admin-Key.
func gqlAs(t *testing.T, url, accountID, adminKey, query string, variables map[string]any) gqlResp {
	t.Helper()
	body, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		t.Fatalf("marshal gql body: %v", err)
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
	return doGQL(t, req)
}

func createDraft(t *testing.T, url, accountID string, variables map[string]any) gqlResp {
	t.Helper()
	return gqlAs(t, url, accountID, "", createMapMutation, variables)
}

func errorCode(resp gqlResp) string {
	if len(resp.Errors) == 0 {
		return ""
	}
	return resp.Errors[0].Extensions["code"].(string)
}

func TestMapMutationsRequireSignIn(t *testing.T) {
	url, _, _ := newPGMapServer(t)
	cases := []struct {
		name  string
		query string
		vars  map[string]any
	}{
		{"createMap", createMapMutation, map[string]any{"name": "guest", "map": mapInput("guest")}},
		{"updateMap", updateMapMutation, map[string]any{"name": "classic", "patch": map[string]any{"description": "x"}}},
		{"deleteMap", deleteMapMutation, map[string]any{"name": "classic"}},
		{"publishMap", publishMapMutation, map[string]any{"name": "classic"}},
		{"claimMap", claimMapMutation, map[string]any{"name": "classic", "editKey": "k"}},
		{"validateMap", `mutation($map: GameMapInput!) { validateMap(map: $map) { valid } }`, map[string]any{"map": mapInput("guest")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := gqlAs(t, url, "", "", tc.query, tc.vars)
			if code := errorCode(resp); code != "UNAUTHENTICATED" {
				t.Fatalf("%s as guest = %v, want UNAUTHENTICATED", tc.name, resp.Errors)
			}
		})
	}
}

func TestDraftLifecycleAndVisibility(t *testing.T) {
	url, _, _ := newPGMapServer(t)

	created := createDraft(t, url, "acct-a", map[string]any{"name": "mine", "map": mapInput("mine")})
	if len(created.Errors) > 0 {
		t.Fatalf("createMap: %v", created.Errors)
	}
	var out struct {
		CreateMap struct {
			Name      string  `json:"name"`
			Builtin   bool    `json:"builtin"`
			Status    string  `json:"status"`
			OwnedByMe bool    `json:"ownedByMe"`
			EditKey   *string `json:"editKey"`
		} `json:"createMap"`
	}
	decodeData(t, created.Data, &out)
	if out.CreateMap.Builtin || out.CreateMap.Status != "DRAFT" || !out.CreateMap.OwnedByMe {
		t.Fatalf("createMap = %+v", out.CreateMap)
	}
	if out.CreateMap.EditKey == nil || *out.CreateMap.EditKey == "" {
		t.Fatal("createMap must return the legacy edit token once")
	}

	// The owner sees the draft; another player neither lists nor reads it.
	if listed := gqlAs(t, url, "acct-a", "", mapsQuery, nil); !hasMap(listed, "mine") {
		t.Fatalf("owner's maps missing the draft: %v", listed.Errors)
	}
	if listed := gqlAs(t, url, "acct-b", "", mapsQuery, nil); hasMap(listed, "mine") {
		t.Fatal("another player must not see the draft")
	}
	if got := gqlAs(t, url, "acct-b", "", mapQuery, map[string]any{"name": "mine"}); errorCode(got) != "NOT_FOUND" {
		t.Fatalf("other player's map query = %v, want NOT_FOUND", got.Errors)
	}
	if got := gqlAs(t, url, "", "", mapQuery, map[string]any{"name": "mine"}); errorCode(got) != "NOT_FOUND" {
		t.Fatalf("guest map query = %v, want NOT_FOUND", got.Errors)
	}

	// Sessions may only be started on it by its owner.
	if got := gqlAs(t, url, "acct-b", "", `mutation { createSession(mapID: "mine") { mapName } }`, nil); errorCode(got) != "NOT_FOUND" {
		t.Fatalf("other player's createSession = %v, want NOT_FOUND", got.Errors)
	}
	if got := gqlAs(t, url, "acct-a", "", `mutation { createSession(mapID: "mine") { mapName gameMap { status ownedByMe } } }`, nil); len(got.Errors) > 0 {
		t.Fatalf("owner's createSession: %v", got.Errors)
	}

	// Another player cannot edit it, even with the legacy token.
	if got := gqlAs(t, url, "acct-b", "", updateMapMutation, map[string]any{
		"name": "mine", "patch": map[string]any{"description": "hijack"}, "editKey": *out.CreateMap.EditKey,
	}); errorCode(got) != "FORBIDDEN" {
		t.Fatalf("other player's update = %v, want FORBIDDEN", got.Errors)
	}
	if got := gqlAs(t, url, "acct-b", "", deleteMapMutation, map[string]any{"name": "mine"}); errorCode(got) != "FORBIDDEN" {
		t.Fatalf("other player's delete = %v, want FORBIDDEN", got.Errors)
	}

	// An owner's save keeps the map a draft.
	updated := gqlAs(t, url, "acct-a", "", updateMapMutation, map[string]any{
		"name": "mine", "patch": map[string]any{"description": "updated"},
	})
	if len(updated.Errors) > 0 {
		t.Fatalf("owner update: %v", updated.Errors)
	}
	if !bytes.Contains(updated.Data, []byte(`"status":"DRAFT"`)) {
		t.Fatalf("owner update should stay DRAFT: %s", updated.Data)
	}

	// Publishing runs the winnability proof: an unwinnable draft is refused.
	if got := createDraft(t, url, "acct-a", map[string]any{"name": "trapped", "map": unwinnableMapInput("trapped")}); len(got.Errors) > 0 {
		t.Fatalf("structurally valid draft rejected: %v", got.Errors)
	}
	if got := gqlAs(t, url, "acct-a", "", "mutation { validateMap(map: "+`{name: "t", description: "", gridSize: 5, maxBattery: 10, startingBattery: 10, layout: ["BBBBB","BHBBB","BBPBB","BBBBB","BBBBB"], legend: [{key: "R", value: "road"},{key: "H", value: "home"},{key: "P", value: "park"},{key: "S", value: "supercharger"},{key: "W", value: "water"},{key: "B", value: "building"}], cellConfigs: []}`+") { valid winnable } }", nil); len(got.Errors) > 0 {
		t.Fatalf("validateMap: %v", got.Errors)
	}
	if got := gqlAs(t, url, "acct-a", "", publishMapMutation, map[string]any{"name": "trapped"}); errorCode(got) != "INVALID_ARGUMENT" {
		t.Fatalf("publishing an unwinnable draft = %v, want INVALID_ARGUMENT", got.Errors)
	}
	if got := gqlAs(t, url, "acct-a", "", publishMapMutation, map[string]any{"name": "mine"}); len(got.Errors) > 0 {
		t.Fatalf("publishing a valid draft: %v", got.Errors)
	}

	// Once published it is visible to everyone and playable by another player.
	if listed := gqlAs(t, url, "acct-b", "", mapsQuery, nil); !hasMap(listed, "mine") {
		t.Fatal("published map missing from another player's listing")
	}
	if got := gqlAs(t, url, "acct-b", "", `mutation { createSession(mapID: "mine") { mapName gameMap { builtin status ownedByMe } } }`, nil); len(got.Errors) > 0 {
		t.Fatalf("createSession on a published map: %v", got.Errors)
	}

	// Only the owner may delete it.
	if got := gqlAs(t, url, "acct-a", "", deleteMapMutation, map[string]any{"name": "mine"}); len(got.Errors) > 0 {
		t.Fatalf("owner delete: %v", got.Errors)
	}
}

func TestShippedMapAdminGate(t *testing.T) {
	url, _, _ := newPGMapServer(t, WithAdminPolicy(appconfig.AdminPolicy{APIKey: "s3cret"}))

	if got := gqlAs(t, url, "acct-a", "", updateMapMutation, map[string]any{
		"name": "classic", "patch": map[string]any{"description": "hacked"},
	}); errorCode(got) != "FORBIDDEN" {
		t.Fatalf("player edit of a shipped map = %v, want FORBIDDEN", got.Errors)
	}
	if got := gqlAs(t, url, "acct-a", "s3cret", updateMapMutation, map[string]any{
		"name": "classic", "patch": map[string]any{"description": "admin edit"},
	}); len(got.Errors) > 0 {
		t.Fatalf("admin edit of a shipped map: %v", got.Errors)
	}
}

func TestLegacyClaimAndAdminCannotTakeOwnership(t *testing.T) {
	url, mgr, _ := newPGMapServer(t)

	// A legacy import: ownerless, with the edit token CreateCustom returns.
	token, err := mgr.CreateCustom("legacy", createValidMapConfig())
	if err != nil {
		t.Fatalf("legacy import: %v", err)
	}
	if token == "" {
		t.Fatal("legacy import did not issue an edit token")
	}

	// A signed-in player with the token may edit the legacy map before claiming.
	if got := gqlAs(t, url, "acct-a", "", updateMapMutation, map[string]any{
		"name": "legacy", "patch": map[string]any{"description": "pre-claim"}, "editKey": token,
	}); len(got.Errors) > 0 {
		t.Fatalf("legacy edit with the token: %v", got.Errors)
	}

	// Claiming attaches it; the token is not echoed back.
	claimed := gqlAs(t, url, "acct-a", "", claimMapMutation, map[string]any{"name": "legacy", "editKey": token})
	if len(claimed.Errors) > 0 {
		t.Fatalf("claimMap: %v", claimed.Errors)
	}
	var out struct {
		ClaimMap struct {
			Status    string  `json:"status"`
			OwnedByMe bool    `json:"ownedByMe"`
			EditKey   *string `json:"editKey"`
		} `json:"claimMap"`
	}
	decodeData(t, claimed.Data, &out)
	if !out.ClaimMap.OwnedByMe || out.ClaimMap.EditKey != nil {
		t.Fatalf("claimMap = %+v", out.ClaimMap)
	}

	// Once owned, the same token never transfers it to another account.
	if got := gqlAs(t, url, "acct-b", "", claimMapMutation, map[string]any{"name": "legacy", "editKey": token}); errorCode(got) != "FORBIDDEN" {
		t.Fatalf("other player's claim = %v, want FORBIDDEN", got.Errors)
	}
}

func hasMap(resp gqlResp, name string) bool {
	if len(resp.Data) == 0 {
		return false
	}
	var out struct {
		Maps []struct {
			Name string `json:"name"`
		} `json:"maps"`
	}
	if err := json.Unmarshal(resp.Data, &out); err != nil {
		return false
	}
	for _, m := range out.Maps {
		if m.Name == name {
			return true
		}
	}
	return false
}

func createValidMapConfig() *engine.GameConfig {
	return &engine.GameConfig{
		Name:            "Legacy",
		GridSize:        5,
		MaxBattery:      10,
		StartingBattery: 10,
		Layout:          []string{"BBBBB", "BRHPB", "BRRSB", "BPPPB", "BBBBB"},
		Legend: map[string]string{
			"R": "road", "H": "home", "P": "park", "S": "supercharger", "W": "water", "B": "building",
		},
	}
}

func viewerContext(acct string) context.Context {
	return auth.WithViewer(context.Background(), &auth.Viewer{Account: &account.Account{ID: acct}})
}

// sessionVisFake serves one catalogue entry to the session-visibility helpers.
type sessionVisFake struct {
	service.GameService
	entry    *service.MapEntry
	entryErr error
	sess     *service.SessionInfo
}

func (f sessionVisFake) GetSession(context.Context, string) (*service.SessionInfo, error) {
	return f.sess, nil
}
func (f sessionVisFake) GetMapEntry(context.Context, string) (*service.MapEntry, error) {
	if f.entryErr != nil {
		return nil, f.entryErr
	}
	return f.entry, nil
}

func TestDraftSessionVisibilityHelpers(t *testing.T) {
	sess := &service.SessionInfo{ID: "s", MapName: "m", OwnerAccountID: "acct-a"}
	ownerless := &service.MapEntry{Name: "m", Status: service.MapStatusDraft}
	r := &Resolver{Service: sessionVisFake{entry: ownerless, sess: sess}}

	if r.sessionVisible(viewerContext(""), sess) {
		t.Fatal("guest must not see a session on an ownerless draft")
	}
	if r.sessionVisible(viewerContext("acct-a"), sess) {
		t.Fatal("a player must not own an ownerless draft")
	}

	owned := &service.MapEntry{Name: "m", Status: service.MapStatusDraft, OwnerID: "acct-a"}
	r = &Resolver{Service: sessionVisFake{entry: owned, sess: sess}}
	if !r.sessionVisible(viewerContext("acct-a"), sess) {
		t.Fatal("the owner must see their draft session")
	}

	// Deleted map: only the non-empty session owner may read it.
	r = &Resolver{Service: sessionVisFake{entryErr: service.ErrMapNotFound, sess: sess}}
	if !r.sessionVisible(viewerContext("acct-a"), sess) {
		t.Fatal("the session owner must see a session whose map was deleted")
	}
	if r.sessionVisible(viewerContext("acct-b"), sess) || r.sessionVisible(viewerContext(""), sess) {
		t.Fatal("another player or guest must not see a deleted-map session")
	}
	legacy := &service.SessionInfo{ID: "old", MapName: "gone"}
	if r.sessionVisible(viewerContext("acct-a"), legacy) {
		t.Fatal("a legacy session without an owner must stay hidden after map deletion")
	}
}

func TestDraftSessionVisibilityOverHTTP(t *testing.T) {
	url, _, res := newPGMapServer(t)
	res.Hub = websocket.NewHub()

	if got := createDraft(t, url, "acct-a", map[string]any{"name": "mine", "map": mapInput("mine")}); len(got.Errors) > 0 {
		t.Fatalf("createMap: %v", got.Errors)
	}
	created := gqlAs(t, url, "acct-a", "", `mutation { createSession(mapID: "mine") { id } }`, nil)
	if len(created.Errors) > 0 {
		t.Fatalf("createSession: %v", created.Errors)
	}
	var sessOut struct {
		CreateSession struct {
			ID string `json:"id"`
		} `json:"createSession"`
	}
	decodeData(t, created.Data, &sessOut)
	id := sessOut.CreateSession.ID
	if id == "" {
		t.Fatal("no session id")
	}

	// The owner reads, lists and plays the session.
	for _, q := range []string{
		`query($id: ID!) { session(id: $id) { id } }`,
		`query($id: ID!) { gameState(sessionID: $id) { battery } }`,
		`query($id: ID!) { history(sessionID: $id) { totalMoves } }`,
	} {
		if got := gqlAs(t, url, "acct-a", "", q, map[string]any{"id": id}); len(got.Errors) > 0 {
			t.Fatalf("owner %s: %v", q, got.Errors)
		}
	}
	if got := gqlAs(t, url, "acct-a", "", `mutation($id: ID!) { move(sessionID: $id, direction: UP) { success } }`, map[string]any{"id": id}); len(got.Errors) > 0 {
		t.Fatalf("owner move: %v", got.Errors)
	}
	if got := gqlAs(t, url, "acct-a", "", `{ sessions { sessions { id } } }`, nil); !containsSession(got, id) {
		t.Fatalf("owner list missing session: %s", got.Data)
	}
	if got := gqlAs(t, url, "acct-a", "", `{ unifiedSessions { sessions { sessionId } } }`, nil); !bytes.Contains(got.Data, []byte(id)) {
		t.Fatalf("owner unified list missing session: %s", got.Data)
	}

	// Another player and guests cannot read, play or list it.
	for _, acct := range []string{"acct-b", ""} {
		for _, q := range []string{
			`query($id: ID!) { session(id: $id) { id } }`,
			`query($id: ID!) { gameState(sessionID: $id) { battery } }`,
			`query($id: ID!) { history(sessionID: $id) { totalMoves } }`,
		} {
			if got := gqlAs(t, url, acct, "", q, map[string]any{"id": id}); errorCode(got) != "NOT_FOUND" {
				t.Fatalf("%q %s = %v, want NOT_FOUND", acct, q, got.Errors)
			}
		}
		if got := gqlAs(t, url, acct, "", `mutation($id: ID!) { move(sessionID: $id, direction: UP) { success } }`, map[string]any{"id": id}); errorCode(got) != "NOT_FOUND" {
			t.Fatalf("%q move = %v, want NOT_FOUND", acct, got.Errors)
		}
		if got := gqlAs(t, url, acct, "", `{ sessions { sessions { id } } }`, nil); containsSession(got, id) {
			t.Fatalf("%q sees a draft session in the list", acct)
		}
		if got := gqlAs(t, url, acct, "", `{ unifiedSessions { sessions { sessionId } } }`, nil); bytes.Contains(got.Data, []byte(id)) {
			t.Fatalf("%q sees a draft session in unifiedSessions", acct)
		}
	}

	// The subscription refuses hidden sessions for non-owners.
	sub := res.Subscription()
	if _, err := sub.SessionUpdated(viewerContext("acct-b"), id); err == nil {
		t.Fatal("subscription to another player's draft session must fail")
	}
	ownerCtx, cancel := context.WithCancel(viewerContext("acct-a"))
	defer cancel()
	if _, err := sub.SessionUpdated(ownerCtx, id); err != nil {
		t.Fatalf("owner subscription: %v", err)
	}

	// Publishing makes the session public again.
	if got := gqlAs(t, url, "acct-a", "", publishMapMutation, map[string]any{"name": "mine"}); len(got.Errors) > 0 {
		t.Fatalf("publishMap: %v", got.Errors)
	}
	if got := gqlAs(t, url, "acct-b", "", `query($id: ID!) { session(id: $id) { id } }`, map[string]any{"id": id}); len(got.Errors) > 0 {
		t.Fatalf("published session for another player: %v", got.Errors)
	}
}

// A session outlives its map. When the map is gone, only the non-empty session
// owner may read it; its status falls back to PUBLISHED so the non-null enum
// stays valid.
func TestDeletedMapSessionVisibilityAndStatus(t *testing.T) {
	url, _, _ := newPGMapServer(t)

	// Draft map: create, play, delete. Guests and other players are hidden.
	if got := createDraft(t, url, "acct-a", map[string]any{"name": "gone", "map": mapInput("gone")}); len(got.Errors) > 0 {
		t.Fatalf("createMap: %v", got.Errors)
	}
	created := gqlAs(t, url, "acct-a", "", `mutation { createSession(mapID: "gone") { id } }`, nil)
	if len(created.Errors) > 0 {
		t.Fatalf("createSession: %v", created.Errors)
	}
	var out struct {
		CreateSession struct {
			ID string `json:"id"`
		} `json:"createSession"`
	}
	decodeData(t, created.Data, &out)
	if got := gqlAs(t, url, "acct-a", "", deleteMapMutation, map[string]any{"name": "gone"}); len(got.Errors) > 0 {
		t.Fatalf("deleteMap: %v", got.Errors)
	}

	sessionQuery := `query($id: ID!) { session(id: $id) { gameMap { status } } }`
	owner := gqlAs(t, url, "acct-a", "", sessionQuery, map[string]any{"id": out.CreateSession.ID})
	if len(owner.Errors) > 0 || !bytes.Contains(owner.Data, []byte(`"status":"PUBLISHED"`)) {
		t.Fatalf("owner session after map deletion = %s %v, want PUBLISHED", owner.Data, owner.Errors)
	}
	for _, acct := range []string{"acct-b", ""} {
		if got := gqlAs(t, url, acct, "", sessionQuery, map[string]any{"id": out.CreateSession.ID}); errorCode(got) != "NOT_FOUND" {
			t.Fatalf("%q session after map deletion = %v, want NOT_FOUND", acct, got.Errors)
		}
	}

	// Published map, then deleted: the creator still sees the session, guests
	// and other players do not. This is intentional privacy after deletion.
	if got := createDraft(t, url, "acct-a", map[string]any{"name": "pubgone", "map": mapInput("pubgone")}); len(got.Errors) > 0 {
		t.Fatalf("createMap: %v", got.Errors)
	}
	if got := gqlAs(t, url, "acct-a", "", publishMapMutation, map[string]any{"name": "pubgone"}); len(got.Errors) > 0 {
		t.Fatalf("publishMap: %v", got.Errors)
	}
	created = gqlAs(t, url, "acct-a", "", `mutation { createSession(mapID: "pubgone") { id } }`, nil)
	if len(created.Errors) > 0 {
		t.Fatalf("createSession: %v", created.Errors)
	}
	decodeData(t, created.Data, &out)
	if got := gqlAs(t, url, "acct-a", "", deleteMapMutation, map[string]any{"name": "pubgone"}); len(got.Errors) > 0 {
		t.Fatalf("deleteMap: %v", got.Errors)
	}
	if got := gqlAs(t, url, "acct-a", "", sessionQuery, map[string]any{"id": out.CreateSession.ID}); len(got.Errors) > 0 {
		t.Fatalf("creator session after published-map deletion: %v", got.Errors)
	}
	for _, acct := range []string{"acct-b", ""} {
		if got := gqlAs(t, url, acct, "", sessionQuery, map[string]any{"id": out.CreateSession.ID}); errorCode(got) != "NOT_FOUND" {
			t.Fatalf("%q session after published-map deletion = %v, want NOT_FOUND", acct, got.Errors)
		}
	}
}

func containsSession(resp gqlResp, id string) bool {
	var out struct {
		Sessions struct {
			Sessions []struct {
				ID string `json:"id"`
			} `json:"sessions"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(resp.Data, &out); err != nil {
		return false
	}
	for _, s := range out.Sessions.Sessions {
		if s.ID == id {
			return true
		}
	}
	return false
}
