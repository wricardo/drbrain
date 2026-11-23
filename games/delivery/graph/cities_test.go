package graph

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/jackc/pgx/v5/pgxpool"

	cityconfig "github.com/wricardo/drbrain/games/delivery/game/config"
	"github.com/wricardo/drbrain/games/delivery/game/engine"
	"github.com/wricardo/drbrain/games/delivery/game/session"
	"github.com/wricardo/drbrain/games/delivery/graph/generated"
	"github.com/wricardo/drbrain/platform/account"
	"github.com/wricardo/drbrain/platform/auth"
	"github.com/wricardo/drbrain/platform/pgdb/pgtest"
)

const cityViewFields = `id name gridSize status ownedByMe builtin ownerless rows`

const (
	createCityMutation = `mutation ($id: ID!, $config: String!) {
		createCity(id: $id, config: $config) { ` + cityViewFields + ` }
	}`
	updateCityMutation = `mutation ($id: ID!, $config: String!) {
		updateCity(id: $id, config: $config) { ` + cityViewFields + ` }
	}`
	publishCityMutation  = `mutation ($id: ID!) { publishCity(id: $id) { ` + cityViewFields + ` } }`
	deleteCityMutation   = `mutation ($id: ID!) { deleteCity(id: $id) }`
	validateCityMutation = `mutation ($config: String!) {
		validateCity(config: $config) { valid publishable issues }
	}`
	citiesQuery               = `query { cities { id name gridSize status ownedByMe builtin ownerless } }`
	cityQuery                 = `query ($id: ID) { city(id: $id) { ` + cityViewFields + ` } }`
	cityConfigQuery           = `query ($id: ID!) { cityConfig(id: $id) }`
	createSessionCityMutation = `mutation ($cityId: ID, $seed: Int) {
		createSession(cityId: $cityId, seed: $seed) { session { id city { id name } } ownerToken }
	}`
)

type testCityEnv struct {
	ts       *httptest.Server
	resolver *Resolver
	catalog  *cityconfig.Manager
	accounts *account.Service
	alice    string
	bob      string
	pool     *pgxpool.Pool
	adminKey string
}

// newTestCityEnv wires the delivery schema over a temp builtin directory (the
// repo city copied as "city" and "alpha") and a fresh PostgreSQL schema.
func newTestCityEnv(t *testing.T, adminKey string, allowUnauthenticatedAdmin bool) *testCityEnv {
	t.Helper()
	pool := pgtest.Pool(t)

	raw, err := os.ReadFile(deliveryCityPath(t))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, name := range []string{"city.json", "alpha.json"} {
		if err := os.WriteFile(filepath.Join(dir, name), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cat, err := cityconfig.Open(cityconfig.Options{
		BuiltinDir:    dir,
		DefaultCityID: "city",
		Pool:          pool,
	})
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	mgr, err := session.NewManager(cat, pool)
	if err != nil {
		t.Fatalf("manager: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Close() })

	accounts := account.New(account.Options{Store: account.NewPostgresStore(pool)})
	signIn := func(name string) string {
		acct, _, err := accounts.SignIn("password", name)
		if err != nil {
			t.Fatalf("sign in %s: %v", name, err)
		}
		return acct.ID
	}

	resolver := &Resolver{
		Mgr:                       mgr,
		Catalog:                   cat,
		Accounts:                  accounts,
		AdminKey:                  adminKey,
		AllowUnauthenticatedAdmin: allowUnauthenticatedAdmin,
	}
	srv := handler.New(generated.NewExecutableSchema(generated.Config{Resolvers: resolver}))
	srv.AddTransport(transport.POST{})
	srv.AddTransport(transport.GET{})
	srv.AddTransport(transport.Options{})

	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := auth.WithRequest(r.Context(), r)
		if id := r.Header.Get("X-Test-Account"); id != "" {
			if a, ok := accounts.Lookup(id); ok {
				ctx = auth.WithViewer(ctx, &auth.Viewer{Account: &a})
			}
		}
		srv.ServeHTTP(w, r.WithContext(ctx))
	})
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)

	return &testCityEnv{
		ts: ts, resolver: resolver, catalog: cat, accounts: accounts,
		alice: signIn("alice"), bob: signIn("bob"), pool: pool,
		adminKey: adminKey,
	}
}

type cityViewJSON struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	GridSize  int      `json:"gridSize"`
	Status    string   `json:"status"`
	OwnedByMe bool     `json:"ownedByMe"`
	Builtin   bool     `json:"builtin"`
	Ownerless bool     `json:"ownerless"`
	Rows      []string `json:"rows"`
}

type cityInfoJSON struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	GridSize  int    `json:"gridSize"`
	Status    string `json:"status"`
	OwnedByMe bool   `json:"ownedByMe"`
	Builtin   bool   `json:"builtin"`
	Ownerless bool   `json:"ownerless"`
}

type cityValidationJSON struct {
	Valid       bool     `json:"valid"`
	Publishable bool     `json:"publishable"`
	Issues      []string `json:"issues"`
}

func cityJSON(name string, withDepot bool) string {
	cfg := &engine.CityConfig{
		Name:                name,
		GridSize:            5,
		TickMS:              300,
		GroundExtraStepCost: 1,
		TicksPerMonth:       600,
		MaxEvents:           500,
		MaxReputation:       5,
		MaxChapter:          5,
		AutosaveTicks:       60,
		Layout: []string{
			".....",
			"#####",
			"##D##",
			"#####",
			".....",
		},
		Legend: map[string]string{
			".": "road",
			"#": "building",
			"D": "depot",
		},
		Orders: engine.OrdersConfig{
			MaxActiveOrders:          6,
			TargetOrdersPerCustomer:  0.75,
			MinSpawnGapTicks:         12,
			DeadlineSlack:            2,
			RewardBulkBase:           0.6,
			RewardBulkPerBox:         0.4,
			ExpireFineDivisor:        4,
			MinRouteTicks:            5,
			MinDeadlineTicks:         10,
			UnreachableLegTicks:      20,
			LatePayMultiplier:        0.6,
			RampTicks:                2400,
			CustomerCooldownMinScale: 0.5,
		},
		Charging: engine.ChargingConfig{DepotRate: 6, ChargerRate: 2},
		Fleet:    &engine.FleetConfig{UpkeepBoxDivisor: 4, UpkeepPerBoxPerInterval: 1, DefaultUpkeepPerInterval: 1},
		Closures: &engine.ClosureConfig{},
	}
	if withDepot {
		cfg.Depots = []engine.DepotConfig{
			{ID: "home", Pos: engine.Position{X: 2, Y: 2}},
		}
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func cityErrCode(res gqlResponse) string {
	if len(res.Errors) == 0 {
		return ""
	}
	code, _ := res.Errors[0].Extensions["code"].(string)
	return code
}

func cityMutationView(t *testing.T, res gqlResponse, field string) cityViewJSON {
	t.Helper()
	if len(res.Errors) > 0 {
		t.Fatalf("%s: %v", field, res.Errors)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(res.Data, &doc); err != nil {
		t.Fatal(err)
	}
	var out cityViewJSON
	if err := json.Unmarshal(doc[field], &out); err != nil {
		t.Fatalf("%s decode: %v", field, err)
	}
	return out
}

func createCityAs(t *testing.T, env *testCityEnv, account, id, config string) cityViewJSON {
	t.Helper()
	return cityMutationView(t, gqlAs(t, env.ts, account, createCityMutation,
		map[string]any{"id": id, "config": config}), "createCity")
}

func publishCityAs(t *testing.T, env *testCityEnv, account, id string, headers map[string]string) gqlResponse {
	t.Helper()
	return gqlAsHeaders(t, env.ts, account, headers, publishCityMutation, map[string]any{"id": id})
}

func TestCityMutationsRequireSignedIn(t *testing.T) {
	env := newTestCityEnv(t, "", false)
	valid := cityJSON("Guest", true)

	cases := []struct {
		name  string
		query string
		vars  map[string]any
	}{
		{"createCity", createCityMutation, map[string]any{"id": "guest", "config": valid}},
		{"updateCity", updateCityMutation, map[string]any{"id": "city", "config": valid}},
		{"publishCity", publishCityMutation, map[string]any{"id": "city"}},
		{"deleteCity", deleteCityMutation, map[string]any{"id": "city"}},
		{"validateCity", validateCityMutation, map[string]any{"config": valid}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := gqlAs(t, env.ts, "", tc.query, tc.vars)
			if got := cityErrCode(res); got != codeUnauthenticated {
				t.Fatalf("code = %q, want %q (message %q)", got, codeUnauthenticated, errText(res))
			}
		})
	}
}

func TestCityOwnerDraftLifecycle(t *testing.T) {
	env := newTestCityEnv(t, "", false)

	playable := createCityAs(t, env, env.alice, "playable", cityJSON("Playable", true))
	if playable.Status != "DRAFT" || !playable.OwnedByMe || playable.GridSize != 5 || playable.Builtin || playable.Ownerless {
		t.Fatalf("create playable = %+v", playable)
	}

	res := gqlAs(t, env.ts, env.alice, citiesQuery, nil)
	if len(res.Errors) > 0 {
		t.Fatal(res.Errors)
	}
	var cities struct{ Cities []cityInfoJSON }
	if err := json.Unmarshal(res.Data, &cities); err != nil {
		t.Fatal(err)
	}
	if !hasCityInfo(cities.Cities, "playable") {
		t.Fatalf("owner's draft missing from cities: %+v", cities.Cities)
	}
	// The owner may start a session on their own draft. The session snapshots it.
	res = gqlAs(t, env.ts, env.alice, createSessionCityMutation, map[string]any{"cityId": "playable", "seed": 4})
	if got := sessionCityID(t, res); got != "playable" {
		t.Fatalf("session city = %q, want playable", got)
	}
	// Guests see shipped/published cities but never a draft.
	res = gqlAs(t, env.ts, "", citiesQuery, nil)
	var guestCities struct{ Cities []cityInfoJSON }
	if err := json.Unmarshal(res.Data, &guestCities); err != nil {
		t.Fatal(err)
	}
	if hasCityInfo(guestCities.Cities, "playable") {
		t.Fatal("guest saw a draft in cities")
	}
	if !hasCityInfo(guestCities.Cities, "city") {
		t.Fatal("guest did not see the shipped city")
	}

	// Duplicate ids and shipped ids are refused.
	res = gqlAs(t, env.ts, env.alice, createCityMutation,
		map[string]any{"id": "playable", "config": cityJSON("Duplicate", true)})
	if got := cityErrCode(res); got != codeInvalidArgument {
		t.Fatalf("duplicate create = %q, want INVALID_ARGUMENT", got)
	}
	res = gqlAs(t, env.ts, env.alice, createCityMutation,
		map[string]any{"id": "city", "config": cityJSON("Shadow", true)})
	if got := cityErrCode(res); got != codeInvalidArgument {
		t.Fatalf("builtin shadow create = %q, want INVALID_ARGUMENT", got)
	}

	// A structurally valid but unplayable draft is accepted and can be edited.
	noDepot := createCityAs(t, env, env.alice, "no-depot", cityJSON("No Depot", false))
	if noDepot.Status != "DRAFT" || len(noDepot.Rows) == 0 {
		t.Fatalf("structural draft = %+v", noDepot)
	}
	res = publishCityAs(t, env, env.alice, "no-depot", nil)
	if got := cityErrCode(res); got != codeInvalidArgument {
		t.Fatalf("publish invalid = %q, want INVALID_ARGUMENT (%q)", got, errText(res))
	}

	updated := cityMutationView(t, gqlAs(t, env.ts, env.alice, updateCityMutation,
		map[string]any{"id": "no-depot", "config": cityJSON("No Depot Fixed", true)}), "updateCity")
	if updated.Status != "DRAFT" {
		t.Fatalf("update status = %q, want DRAFT", updated.Status)
	}
	published := cityMutationView(t, publishCityAs(t, env, env.alice, "no-depot", nil), "publishCity")
	if published.Status != "PUBLISHED" || !published.OwnedByMe {
		t.Fatalf("published = %+v", published)
	}

	// Editing a published city puts it back into DRAFT.
	updated = cityMutationView(t, gqlAs(t, env.ts, env.alice, updateCityMutation,
		map[string]any{"id": "no-depot", "config": cityJSON("No Depot Again", true)}), "updateCity")
	if updated.Status != "DRAFT" {
		t.Fatalf("edit after publish = %q, want DRAFT", updated.Status)
	}
}

func TestCityOtherPlayerCannotSeeOrChangeDraft(t *testing.T) {
	env := newTestCityEnv(t, "", false)
	createCityAs(t, env, env.alice, "secret", cityJSON("Secret", true))

	// Hidden from the other player's list and detail query.
	res := gqlAs(t, env.ts, env.bob, citiesQuery, nil)
	var cities struct{ Cities []cityInfoJSON }
	if err := json.Unmarshal(res.Data, &cities); err != nil {
		t.Fatal(err)
	}
	if hasCityInfo(cities.Cities, "secret") {
		t.Fatal("other player saw a draft in cities")
	}
	res = gqlAs(t, env.ts, env.bob, cityQuery, map[string]any{"id": "secret"})
	var detail struct{ City *cityViewJSON }
	if err := json.Unmarshal(res.Data, &detail); err != nil {
		t.Fatal(err)
	}
	if detail.City != nil {
		t.Fatalf("other player saw the draft: %+v", detail.City)
	}

	// Hidden drafts are indistinguishable from missing cities on mutations.
	for _, tc := range []struct {
		name  string
		query string
		vars  map[string]any
	}{
		{"createSession", createSessionCityMutation, map[string]any{"cityId": "secret", "seed": 1}},
		{"updateCity", updateCityMutation, map[string]any{"id": "secret", "config": cityJSON("Stolen", true)}},
		{"publishCity", publishCityMutation, map[string]any{"id": "secret"}},
		{"deleteCity", deleteCityMutation, map[string]any{"id": "secret"}},
	} {
		res := gqlAs(t, env.ts, env.bob, tc.query, tc.vars)
		if got := cityErrCode(res); got != codeNotFound {
			t.Errorf("%s code = %q, want NOT_FOUND (%q)", tc.name, got, errText(res))
		}
	}

	// Once published the city is visible, playable, but still not writable.
	published := cityMutationView(t, publishCityAs(t, env, env.alice, "secret", nil), "publishCity")
	if published.Status != "PUBLISHED" {
		t.Fatalf("publish = %+v", published)
	}
	res = gqlAs(t, env.ts, env.bob, citiesQuery, nil)
	cities = struct{ Cities []cityInfoJSON }{}
	if err := json.Unmarshal(res.Data, &cities); err != nil {
		t.Fatal(err)
	}
	if !hasCityInfo(cities.Cities, "secret") {
		t.Fatal("published city is not listed for another player")
	}
	created := gqlAs(t, env.ts, env.bob, createSessionCityMutation, map[string]any{"cityId": "secret", "seed": 2})
	if len(created.Errors) > 0 {
		t.Fatalf("another player could not play a published city: %v", created.Errors)
	}

	for _, tc := range []struct {
		name  string
		query string
		vars  map[string]any
	}{
		{"updateCity", updateCityMutation, map[string]any{"id": "secret", "config": cityJSON("Stolen", true)}},
		{"publishCity", publishCityMutation, map[string]any{"id": "secret"}},
		{"deleteCity", deleteCityMutation, map[string]any{"id": "secret"}},
	} {
		res := gqlAs(t, env.ts, env.bob, tc.query, tc.vars)
		if got := cityErrCode(res); got != codeForbidden {
			t.Errorf("%s code = %q, want FORBIDDEN (%q)", tc.name, got, errText(res))
		}
	}
}

func TestCityAdminBuiltinAndLegacyGates(t *testing.T) {
	env := newTestCityEnv(t, "sekret", false)

	// Shipped cities are listed for everyone, ownerless.
	res := gqlAs(t, env.ts, env.alice, citiesQuery, nil)
	var list struct{ Cities []cityInfoJSON }
	if err := json.Unmarshal(res.Data, &list); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"city", "alpha"} {
		info, ok := findCityInfo(list.Cities, id)
		if !ok {
			t.Fatalf("shipped city %q missing", id)
		}
		if info.Status != "PUBLISHED" || info.OwnedByMe || !info.Builtin || info.Ownerless {
			t.Fatalf("shipped %q = %+v", id, info)
		}
	}

	// A shipped city needs the admin key.
	res = gqlAs(t, env.ts, env.bob, updateCityMutation,
		map[string]any{"id": "city", "config": cityJSON("Hijacked", true)})
	if got := cityErrCode(res); got != codeForbidden {
		t.Fatalf("builtin update without key = %q, want FORBIDDEN", got)
	}
	res = gqlAsHeaders(t, env.ts, env.bob, map[string]string{"X-Admin-Key": "wrong"}, updateCityMutation,
		map[string]any{"id": "city", "config": cityJSON("Hijacked", true)})
	if got := cityErrCode(res); got != codeForbidden {
		t.Fatalf("builtin update with wrong key = %q, want FORBIDDEN", got)
	}

	edited := cityMutationView(t, gqlAsHeaders(t, env.ts, env.bob,
		map[string]string{"X-Admin-Key": "sekret"}, updateCityMutation,
		map[string]any{"id": "city", "config": cityJSON("Admin Edited", true)}), "updateCity")
	if edited.Status != "PUBLISHED" || edited.OwnedByMe || edited.Name != "Admin Edited" {
		t.Fatalf("admin builtin update = %+v", edited)
	}
	res = gqlAs(t, env.ts, env.alice, cityQuery, map[string]any{"id": "city"})
	var detail struct{ City *cityViewJSON }
	if err := json.Unmarshal(res.Data, &detail); err != nil {
		t.Fatal(err)
	}
	if detail.City == nil || detail.City.Name != "Admin Edited" {
		t.Fatalf("builtin view after admin edit = %+v", detail.City)
	}

	// The configured default cannot be deleted, even by an admin.
	res = gqlAsHeaders(t, env.ts, env.bob, map[string]string{"X-Admin-Key": "sekret"}, deleteCityMutation,
		map[string]any{"id": "city"})
	if got := cityErrCode(res); got != codeInvalidArgument {
		t.Fatalf("default delete = %q, want INVALID_ARGUMENT (%q)", got, errText(res))
	}
	if !strings.Contains(errText(res), "default") {
		t.Fatalf("default delete message = %q", errText(res))
	}

	// Legacy ownerless rows are also admin-only, and an admin update preserves
	// their PUBLISHED status and empty owner.
	insertCityRow(t, env.pool, "legacy", cityJSON("Legacy", true), "", "PUBLISHED")
	res = gqlAs(t, env.ts, env.bob, updateCityMutation,
		map[string]any{"id": "legacy", "config": cityJSON("Legacy Edited", true)})
	if got := cityErrCode(res); got != codeForbidden {
		t.Fatalf("legacy update without key = %q, want FORBIDDEN", got)
	}
	legacy := cityMutationView(t, gqlAsHeaders(t, env.ts, env.bob,
		map[string]string{"X-Admin-Key": "sekret"}, updateCityMutation,
		map[string]any{"id": "legacy", "config": cityJSON("Legacy Edited", true)}), "updateCity")
	if legacy.Status != "PUBLISHED" || legacy.OwnedByMe || legacy.Builtin || !legacy.Ownerless {
		t.Fatalf("legacy admin update = %+v", legacy)
	}
	res = gqlAs(t, env.ts, env.bob, deleteCityMutation, map[string]any{"id": "legacy"})
	if got := cityErrCode(res); got != codeForbidden {
		t.Fatalf("legacy delete without key = %q, want FORBIDDEN", got)
	}
	res = gqlAsHeaders(t, env.ts, env.bob, map[string]string{"X-Admin-Key": "sekret"}, deleteCityMutation,
		map[string]any{"id": "legacy"})
	if len(res.Errors) > 0 {
		t.Fatalf("admin legacy delete: %v", res.Errors)
	}

	// An admin may also remove a non-default shipped city.
	res = gqlAsHeaders(t, env.ts, env.bob, map[string]string{"X-Admin-Key": "sekret"}, deleteCityMutation,
		map[string]any{"id": "alpha"})
	if len(res.Errors) > 0 {
		t.Fatalf("admin builtin delete: %v", res.Errors)
	}
	res = gqlAs(t, env.ts, env.alice, citiesQuery, nil)
	list = struct{ Cities []cityInfoJSON }{}
	if err := json.Unmarshal(res.Data, &list); err != nil {
		t.Fatal(err)
	}
	if hasCityInfo(list.Cities, "alpha") {
		t.Fatal("deleted shipped city still listed")
	}
}

func TestAdminOptOutAllowsKeylessAdmin(t *testing.T) {
	env := newTestCityEnv(t, "", true)
	edited := cityMutationView(t, gqlAs(t, env.ts, env.bob, updateCityMutation,
		map[string]any{"id": "alpha", "config": cityJSON("Keyless Edit", true)}), "updateCity")
	if edited.Status != "PUBLISHED" || edited.Name != "Keyless Edit" {
		t.Fatalf("keyless admin update = %+v", edited)
	}
}

func TestCityConfigReadVisibility(t *testing.T) {
	env := newTestCityEnv(t, "", false)
	createCityAs(t, env, env.alice, "raw-draft", cityJSON("Raw Draft", true))

	read := func(account, id string) *string {
		t.Helper()
		res := gqlAs(t, env.ts, account, cityConfigQuery, map[string]any{"id": id})
		if len(res.Errors) > 0 {
			t.Fatalf("cityConfig %s: %v", id, res.Errors)
		}
		var out struct{ CityConfig *string }
		if err := json.Unmarshal(res.Data, &out); err != nil {
			t.Fatal(err)
		}
		return out.CityConfig
	}

	// The owner sees the authored document.
	raw := read(env.alice, "raw-draft")
	if raw == nil {
		t.Fatal("owner got null config")
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(*raw), &doc); err != nil {
		t.Fatalf("config is not JSON: %v", err)
	}
	if doc["name"] != "Raw Draft" || doc["grid_size"] != float64(5) {
		t.Fatalf("config doc = %+v", doc)
	}
	if _, ok := doc["depots"]; !ok {
		t.Fatalf("config doc lost depots: %+v", doc)
	}

	// Another player's draft is indistinguishable from a missing city.
	if got := read(env.bob, "raw-draft"); got != nil {
		t.Fatalf("other player read a draft: %q", *got)
	}
	if got := read(env.bob, "missing"); got != nil {
		t.Fatalf("missing city returned %q", *got)
	}

	// Builtins and published cities are public.
	if got := read("", "city"); got == nil {
		t.Fatal("guest could not read a builtin config")
	}
	published := cityMutationView(t, publishCityAs(t, env, env.alice, "raw-draft", nil), "publishCity")
	if published.Status != "PUBLISHED" {
		t.Fatalf("publish = %+v", published)
	}
	if got := read("", "raw-draft"); got == nil {
		t.Fatal("guest could not read a published config")
	}
}

func TestCreateSessionCitySelection(t *testing.T) {
	env := newTestCityEnv(t, "", false)

	// Omitted cityId uses the default.
	res := gqlAs(t, env.ts, env.alice, createSessionCityMutation, map[string]any{"seed": 1})
	if len(res.Errors) > 0 {
		t.Fatal(res.Errors)
	}
	if got := sessionCityID(t, res); got != "city" {
		t.Fatalf("default session city = %q, want city", got)
	}

	// A second builtin can be selected explicitly.
	res = gqlAs(t, env.ts, env.alice, createSessionCityMutation, map[string]any{"cityId": "alpha", "seed": 2})
	if got := sessionCityID(t, res); got != "alpha" {
		t.Fatalf("alpha session city = %q", got)
	}

	// A published community city can be selected too.
	createCityAs(t, env, env.alice, "community", cityJSON("Community", true))
	if _, err := env.catalog.Publish("community", cityconfig.Actor{AccountID: env.alice}); err != nil {
		t.Fatal(err)
	}
	res = gqlAs(t, env.ts, env.bob, createSessionCityMutation, map[string]any{"cityId": "community", "seed": 3})
	if got := sessionCityID(t, res); got != "community" {
		t.Fatalf("community session city = %q", got)
	}
}

func TestValidateCity(t *testing.T) {
	env := newTestCityEnv(t, "", false)

	validate := func(config string) cityValidationJSON {
		t.Helper()
		res := gqlAs(t, env.ts, env.alice, validateCityMutation, map[string]any{"config": config})
		if len(res.Errors) > 0 {
			t.Fatalf("validate: %v", res.Errors)
		}
		var out struct{ ValidateCity cityValidationJSON }
		if err := json.Unmarshal(res.Data, &out); err != nil {
			t.Fatal(err)
		}
		return out.ValidateCity
	}

	full := validate(cityJSON("Valid", true))
	if !full.Valid || !full.Publishable || len(full.Issues) != 0 {
		t.Fatalf("full valid = %+v", full)
	}
	draft := validate(cityJSON("Draft Only", false))
	if !draft.Valid || draft.Publishable || len(draft.Issues) == 0 {
		t.Fatalf("structural draft = %+v", draft)
	}
	malformed := validate(`{"name":"broken","layout":[`)
	if malformed.Valid || malformed.Publishable || len(malformed.Issues) == 0 {
		t.Fatalf("malformed = %+v", malformed)
	}
}

func sessionCityID(t *testing.T, res gqlResponse) string {
	t.Helper()
	if len(res.Errors) > 0 {
		t.Fatalf("createSession: %v", res.Errors)
	}
	var out struct {
		CreateSession struct {
			Session struct {
				City struct{ ID string }
			}
		}
	}
	if err := json.Unmarshal(res.Data, &out); err != nil {
		t.Fatal(err)
	}
	return out.CreateSession.Session.City.ID
}

func hasCityInfo(list []cityInfoJSON, id string) bool {
	_, ok := findCityInfo(list, id)
	return ok
}

func findCityInfo(list []cityInfoJSON, id string) (cityInfoJSON, bool) {
	for _, c := range list {
		if c.ID == id {
			return c, true
		}
	}
	return cityInfoJSON{}, false
}

func insertCityRow(t *testing.T, pool *pgxpool.Pool, id, config, owner, status string) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
INSERT INTO platform_maps (game, id, config, owner_id, status)
VALUES ('delivery', $1, $2::jsonb, $3, $4)`, id, config, owner, status)
	if err != nil {
		t.Fatalf("insert %s: %v", id, err)
	}
}

func (e *testCityEnv) viewerContext(t *testing.T, accountID string) (context.Context, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	if accountID == "" {
		return ctx, cancel
	}
	a, ok := e.accounts.Lookup(accountID)
	if !ok {
		cancel()
		t.Fatalf("account %s not found", accountID)
	}
	return auth.WithViewer(ctx, &auth.Viewer{Account: &a}), cancel
}

func createSessionOnCity(t *testing.T, env *testCityEnv, account, cityID string) (string, string) {
	t.Helper()
	res := gqlAs(t, env.ts, account, createSessionCityMutation, map[string]any{"cityId": cityID, "seed": 11})
	if len(res.Errors) > 0 {
		t.Fatalf("createSession on %s: %v", cityID, res.Errors)
	}
	var out struct {
		CreateSession struct {
			Session    struct{ ID string }
			OwnerToken string `json:"ownerToken"`
		}
	}
	if err := json.Unmarshal(res.Data, &out); err != nil {
		t.Fatal(err)
	}
	return out.CreateSession.Session.ID, out.CreateSession.OwnerToken
}

func sessionIDs(t *testing.T, res gqlResponse) []string {
	t.Helper()
	if len(res.Errors) > 0 {
		t.Fatalf("sessions: %v", res.Errors)
	}
	var out struct {
		Sessions []struct{ ID string }
	}
	if err := json.Unmarshal(res.Data, &out); err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(out.Sessions))
	for _, s := range out.Sessions {
		ids = append(ids, s.ID)
	}
	return ids
}

func containsID(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func TestDraftCitySessionVisibility(t *testing.T) {
	env := newTestCityEnv(t, "", false)
	createCityAs(t, env, env.alice, "private-city", cityJSON("Private City", true))
	sid, ownerToken := createSessionOnCity(t, env, env.alice, "private-city")

	read := func(account, id string) gqlResponse {
		t.Helper()
		return gqlAs(t, env.ts, account, `query ($id: ID!) { session(id: $id) { id } }`, map[string]any{"id": id})
	}

	// The owner can read and list their draft-city session. The owner token also
	// keeps mutations working from a browser that is not signed in: the read
	// gate is deliberately separate from session authorization.
	if res := read(env.alice, sid); len(res.Errors) > 0 {
		t.Fatalf("owner session read: %v", res.Errors)
	}
	tick := gqlAs(t, env.ts, "", `mutation ($id: ID!, $t: String) { tick(id: $id, count: 1, ownerToken: $t) { tick } }`,
		map[string]any{"id": sid, "t": ownerToken})
	if len(tick.Errors) > 0 {
		t.Fatalf("owner-token mutation on draft-city session: %v", tick.Errors)
	}
	if ids := sessionIDs(t, gqlAs(t, env.ts, env.alice, `query { sessions { id } }`, nil)); !containsID(ids, sid) {
		t.Fatal("owner's draft-city session missing from sessions")
	}

	// Guests and other players get not-found, and the list omits the row.
	for _, account := range []string{"", env.bob} {
		if res := read(account, sid); len(res.Errors) == 0 || !strings.Contains(errText(res), "not_found") {
			t.Fatalf("account %q read a draft-city session: %v", account, res.Errors)
		}
		ids := sessionIDs(t, gqlAs(t, env.ts, account, `query { sessions { id } }`, nil))
		if containsID(ids, sid) {
			t.Fatalf("account %q saw the draft-city session in sessions", account)
		}
		for _, q := range []string{
			`query ($id: ID!) { carView(id: $id, carId: "car_1") { rowSpan } }`,
			`query ($id: ID!) { events(id: $id) { oldestTick } }`,
		} {
			res := gqlAs(t, env.ts, account, q, map[string]any{"id": sid})
			if !strings.Contains(errText(res), "not_found") {
				t.Fatalf("account %q read draft city via %q: %v", account, q, res.Errors)
			}
		}
		res := gqlAs(t, env.ts, account, `query ($id: ID!) { canPlaySession(id: $id) }`, map[string]any{"id": sid})
		var out struct{ CanPlaySession bool }
		if len(res.Errors) > 0 {
			t.Fatalf("canPlaySession: %v", res.Errors)
		}
		if err := json.Unmarshal(res.Data, &out); err != nil {
			t.Fatal(err)
		}
		if out.CanPlaySession {
			t.Fatalf("account %q reported it can play a draft-city session", account)
		}
	}

	// Subscription is the same gate.
	guestCtx, guestCancel := env.viewerContext(t, "")
	defer guestCancel()
	if _, err := env.resolver.Subscription().Session(guestCtx, sid); err == nil || !strings.Contains(err.Error(), "not_found") {
		t.Fatalf("guest subscription on draft city = %v", err)
	}
	ownerCtx, ownerCancel := env.viewerContext(t, env.alice)
	ch, err := env.resolver.Subscription().Session(ownerCtx, sid)
	if err != nil {
		t.Fatalf("owner subscription: %v", err)
	}
	ownerCancel()
	if ch == nil {
		t.Fatal("owner subscription returned no channel")
	}

	// Publishing the city makes the same session public.
	if published := cityMutationView(t, publishCityAs(t, env, env.alice, "private-city", nil), "publishCity"); published.Status != "PUBLISHED" {
		t.Fatalf("publish = %+v", published)
	}
	if res := read("", sid); len(res.Errors) > 0 {
		t.Fatalf("guest could not read a published-city session: %v", res.Errors)
	}
	if ids := sessionIDs(t, gqlAs(t, env.ts, "", `query { sessions { id } }`, nil)); !containsID(ids, sid) {
		t.Fatal("published-city session missing from guest sessions")
	}
}
