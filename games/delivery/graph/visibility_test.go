package graph

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestSessionsLimitCountsVisibleRows pins the fix for the limit running before
// the draft-city visibility filter: a hidden newer save must not consume the
// caller's page and hide an older visible one.
func TestSessionsLimitCountsVisibleRows(t *testing.T) {
	env := newTestCityEnv(t, "", false)
	visible, _ := createSessionOnCity(t, env, env.alice, "city")

	createCityAs(t, env, env.bob, "bob-draft", cityJSON("Bob Draft", true))
	hidden, _ := createSessionOnCity(t, env, env.bob, "bob-draft")

	ids := sessionIDs(t, gqlAs(t, env.ts, env.alice, `query { sessions(limit: 1) { id } }`, nil))
	if len(ids) != 1 || ids[0] != visible || containsID(ids, hidden) {
		t.Fatalf("sessions(limit: 1) = %v, want the visible row %s (hidden %s)", ids, visible, hidden)
	}
}

// TestDraftCityAnalyticsAndCanPlay covers the analytics side channel and the
// owner-token answer: a draft-city session must not appear in money/items/
// reputation history for another viewer, and its token holder must still be
// told it can play.
func TestDraftCityAnalyticsAndCanPlay(t *testing.T) {
	env := newTestCityEnv(t, "", false)
	createCityAs(t, env, env.alice, "secret-city", cityJSON("Secret City", true))
	sid, token := createSessionOnCity(t, env, env.alice, "secret-city")

	advanceTicks(t, env.resolver.Mgr, sid, 1)
	if res := gqlAs(t, env.ts, env.alice,
		`mutation ($id: ID!, $t: String) { saveSession(id: $id, ownerToken: $t) { id } }`,
		map[string]any{"id": sid, "t": token}); len(res.Errors) > 0 {
		t.Fatalf("save: %v", res.Errors)
	}

	seriesIDs := func(account, metric string) []string {
		t.Helper()
		q := `query ($id: ID!) { rows: metricHistory(metric: ` + metric + `, ids: [$id]) { sessionId } }`
		res := gqlAs(t, env.ts, account, q, map[string]any{"id": sid})
		if len(res.Errors) > 0 {
			t.Fatalf("%s as %q: %v", metric, account, res.Errors)
		}
		var out struct {
			Rows []struct{ SessionID string } `json:"rows"`
		}
		if err := json.Unmarshal(res.Data, &out); err != nil {
			t.Fatal(err)
		}
		ids := make([]string, 0, len(out.Rows))
		for _, r := range out.Rows {
			ids = append(ids, r.SessionID)
		}
		return ids
	}

	for _, metric := range []string{"MONEY", "BOXES_DELIVERED", "REPUTATION"} {
		if ids := seriesIDs(env.alice, metric); !containsID(ids, sid) {
			t.Fatalf("owner %s = %v, want %s", metric, ids, sid)
		}
		for _, account := range []string{"", env.bob} {
			if ids := seriesIDs(account, metric); containsID(ids, sid) {
				t.Fatalf("account %q read draft session via %s", account, metric)
			}
		}
	}

	// With ids omitted, the newest-20 window must not enumerate the draft.
	res := gqlAs(t, env.ts, "", `query { rows: metricHistory(metric: MONEY) { sessionId } }`, nil)
	if len(res.Errors) > 0 {
		t.Fatalf("guest metricHistory: %v", res.Errors)
	}
	var all struct {
		Rows []struct{ SessionID string } `json:"rows"`
	}
	if err := json.Unmarshal(res.Data, &all); err != nil {
		t.Fatal(err)
	}
	for _, row := range all.Rows {
		if row.SessionID == sid {
			t.Fatal("guest enumerated a draft-city session through metricHistory")
		}
	}

	// The token proves the caller can mutate the session, so canPlaySession
	// must agree even though the city is hidden from that guest.
	res = gqlAs(t, env.ts, "",
		`query ($id: ID!, $t: String) { canPlaySession(id: $id, ownerToken: $t) }`,
		map[string]any{"id": sid, "t": token})
	if len(res.Errors) > 0 {
		t.Fatalf("canPlaySession with token: %v", res.Errors)
	}
	var play struct {
		CanPlaySession bool `json:"canPlaySession"`
	}
	if err := json.Unmarshal(res.Data, &play); err != nil {
		t.Fatal(err)
	}
	if !play.CanPlaySession {
		t.Fatal("owner-token holder reported unable to play its draft-city session")
	}
}

// TestSubscriptionReleasesWatcherOnCancel pins the disconnect path: a paused
// session publishes nothing, so the subscription goroutine must wake on ctx.
func TestSubscriptionReleasesWatcherOnCancel(t *testing.T) {
	env := newTestCityEnv(t, "", false)
	sid, _ := createSessionOnCity(t, env, env.alice, "city")

	ctx, cancel := env.viewerContext(t, "")
	ch, err := env.resolver.Subscription().Session(ctx, sid)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("no eager snapshot")
	}
	cancel()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		sess, ok := env.resolver.Mgr.Get(sid)
		if ok && sess.WatchCount() == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("subscription watcher still attached after the client cancelled")
}

// TestSessionsCursorPaging walks every page with the returned cursor and
// proves no row repeats or disappears, including rows tied on the sort key.
func TestSessionsCursorPaging(t *testing.T) {
	env := newTestCityEnv(t, "", false)
	want := map[string]bool{}
	for i := 0; i < 5; i++ {
		sid, _ := createSessionOnCity(t, env, env.alice, "city")
		want[sid] = true
	}

	seen := map[string]bool{}
	var cursor *string
	for page := 0; page < 10; page++ {
		q := `query ($c: String) { sessions(sort: CREATED_AT, limit: 2, cursor: $c) { id cursor } }`
		res := gqlAs(t, env.ts, env.alice, q, map[string]any{"c": cursor})
		if len(res.Errors) > 0 {
			t.Fatalf("page %d: %v", page, res.Errors)
		}
		var out struct {
			Sessions []struct {
				ID     string
				Cursor string
			}
		}
		if err := json.Unmarshal(res.Data, &out); err != nil {
			t.Fatal(err)
		}
		if len(out.Sessions) == 0 {
			break
		}
		for _, row := range out.Sessions {
			if seen[row.ID] {
				t.Fatalf("row %s repeated across pages", row.ID)
			}
			seen[row.ID] = true
		}
		last := out.Sessions[len(out.Sessions)-1].Cursor
		cursor = &last
	}
	if len(seen) != len(want) {
		t.Fatalf("paged %d sessions, want %d", len(seen), len(want))
	}
	for id := range want {
		if !seen[id] {
			t.Fatalf("session %s never appeared", id)
		}
	}

	// A before filter is created-time only and cannot drive a TICK order.
	res := gqlAs(t, env.ts, env.alice,
		`query { sessions(sort: TICK, before: "2026-01-01T00:00:00Z") { id } }`, nil)
	if len(res.Errors) == 0 || !strings.Contains(errText(res), "before is only valid") {
		t.Fatalf("before with TICK = %v", res.Errors)
	}
}

// TestDeleteCityRemovesSessions pins the chosen policy: sessions die with
// their city, so a deleted draft cannot declassify its saves.
func TestDeleteCityRemovesSessions(t *testing.T) {
	env := newTestCityEnv(t, "", false)
	createCityAs(t, env, env.alice, "doomed-city", cityJSON("Doomed City", true))
	sid, token := createSessionOnCity(t, env, env.alice, "doomed-city")
	if res := gqlAs(t, env.ts, env.alice,
		`mutation ($id: ID!, $t: String) { saveSession(id: $id, ownerToken: $t) { id } }`,
		map[string]any{"id": sid, "t": token}); len(res.Errors) > 0 {
		t.Fatalf("save: %v", res.Errors)
	}
	if res := gqlAs(t, env.ts, env.alice, `mutation ($id: ID!) { deleteCity(id: $id) }`,
		map[string]any{"id": "doomed-city"}); len(res.Errors) > 0 {
		t.Fatalf("deleteCity: %v", res.Errors)
	}
	res := gqlAs(t, env.ts, env.alice, `query ($id: ID!) { session(id: $id) { id } }`,
		map[string]any{"id": sid})
	if len(res.Errors) == 0 || !strings.Contains(errText(res), "not_found") {
		t.Fatalf("session survived its city: %v", res.Errors)
	}
	if ids := sessionIDs(t, gqlAs(t, env.ts, env.alice, `query { sessions { id } }`, nil)); containsID(ids, sid) {
		t.Fatalf("list still shows deleted city's session: %v", ids)
	}
}

// TestSnapshotCityStatus pins the enriched city metadata: the session view
// must agree with city(id) instead of always claiming PUBLISHED.
func TestSnapshotCityStatus(t *testing.T) {
	env := newTestCityEnv(t, "", false)
	createCityAs(t, env, env.alice, "my-draft", cityJSON("My Draft", true))
	draftSID, _ := createSessionOnCity(t, env, env.alice, "my-draft")
	builtinSID, _ := createSessionOnCity(t, env, env.alice, "city")

	read := func(id string) struct {
		Status    string
		OwnedByMe bool
		Builtin   bool
		Ownerless bool
	} {
		t.Helper()
		res := gqlAs(t, env.ts, env.alice,
			`query ($id: ID!) { session(id: $id) { city { status ownedByMe builtin ownerless } } }`,
			map[string]any{"id": id})
		if len(res.Errors) > 0 {
			t.Fatalf("session %s: %v", id, res.Errors)
		}
		var out struct {
			Session struct {
				City struct {
					Status    string
					OwnedByMe bool
					Builtin   bool
					Ownerless bool
				}
			}
		}
		if err := json.Unmarshal(res.Data, &out); err != nil {
			t.Fatal(err)
		}
		return out.Session.City
	}
	if got := read(draftSID); got.Status != "DRAFT" || !got.OwnedByMe || got.Builtin || got.Ownerless {
		t.Fatalf("draft session city = %+v", got)
	}
	if got := read(builtinSID); got.Status != "PUBLISHED" || got.OwnedByMe || !got.Builtin || got.Ownerless {
		t.Fatalf("builtin session city = %+v", got)
	}
}

// TestMutationLookupErrorsAreNotFound covers the engine sentinels: a missing
// car or order is NOT_FOUND, not a conflict or bad request.
func TestMutationLookupErrorsAreNotFound(t *testing.T) {
	env := newTestCityEnv(t, "", false)
	sid, token := createSessionOnCity(t, env, env.alice, "city")

	cases := []string{
		`mutation ($id: ID!, $t: String) { moveCar(id: $id, carId: "nope", direction: NORTH, ownerToken: $t) { id } }`,
		`mutation ($id: ID!, $t: String) { assignOrder(id: $id, orderId: "nope", carId: "nope", ownerToken: $t) { id } }`,
		`mutation ($id: ID!, $t: String) { towCar(id: $id, carId: "nope", ownerToken: $t) { id } }`,
	}
	for _, q := range cases {
		res := gqlAs(t, env.ts, env.alice, q, map[string]any{"id": sid, "t": token})
		if len(res.Errors) == 0 || !strings.Contains(errText(res), "not_found:") {
			t.Fatalf("%s = %v", q, res.Errors)
		}
	}
}
