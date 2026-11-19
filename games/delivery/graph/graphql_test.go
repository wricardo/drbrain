package graph

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wricardo/drbrain/games/delivery/game/engine"
	"github.com/wricardo/drbrain/games/delivery/game/session"
)

// testRoute plans a shortest path across rendered glyph rows and returns the
// directions to drive.
//
// It reasons about the same rows the client receives, so it routes around
// buildings without knowing the map. Test-only: the engine has no pathfinding
// on the player's behalf by design (D1), and this must never grow into a game
// feature.
func testRoute(rows []string, from, to engine.Position) []engine.Direction {
	if from == to {
		return nil
	}
	walkable := func(p engine.Position) bool {
		if p.Y < 0 || p.Y >= len(rows) || p.X < 0 || p.X >= len(rows[p.Y]) {
			return false
		}
		switch rows[p.Y][p.X] {
		case '.', ',', 'D', 'C', 'K', 'H', '1', '2', '3', '4', '5', '6', '7', '8', '9':
			return true
		}
		return false
	}
	type node struct {
		p    engine.Position
		path []engine.Direction
	}
	seen := map[engine.Position]bool{from: true}
	queue := []node{{p: from}}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, d := range engine.AllDirections {
			n, ok := cur.p.Neighbour(d)
			if !ok || seen[n] || !walkable(n) {
				continue
			}
			path := append(append([]engine.Direction(nil), cur.path...), d)
			if n == to {
				return path
			}
			seen[n] = true
			queue = append(queue, node{p: n, path: path})
		}
	}
	return nil
}

// newTestServer wires the real GraphQL handler over a temp save dir.
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()

	city, err := filepath.Abs(filepath.Join("..", "maps", "city.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(city); err != nil {
		t.Fatalf("city file: %v", err)
	}
	mgr, err := session.NewManager(city, t.TempDir())
	if err != nil {
		t.Fatalf("manager: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Close() })

	ts := httptest.NewServer(Handler(mgr))
	t.Cleanup(ts.Close)
	return ts
}

// gqlResponse is the GraphQL envelope.
type gqlResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// query posts a GraphQL operation and returns the decoded envelope.
func query(t *testing.T, ts *httptest.Server, q string, vars map[string]any) gqlResponse {
	t.Helper()
	body, err := json.Marshal(map[string]any{"query": q, "variables": vars})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(ts.URL, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var out gqlResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return out
}

// mustQuery fails the test on any GraphQL error and returns data.
func mustQuery(t *testing.T, ts *httptest.Server, q string, vars map[string]any, into any) {
	t.Helper()
	res := query(t, ts, q, vars)
	if len(res.Errors) > 0 {
		t.Fatalf("graphql errors: %v", res.Errors)
	}
	if into == nil {
		return
	}
	if err := json.Unmarshal(res.Data, into); err != nil {
		t.Fatalf("decode data: %v (raw: %s)", err, res.Data)
	}
}

func TestHealthQuery(t *testing.T) {
	ts := newTestServer(t)
	var out struct {
		Health struct{ Status string }
	}
	mustQuery(t, ts, `{ health { status } }`, nil, &out)
	if out.Health.Status != "ok" {
		t.Fatalf("status = %q", out.Health.Status)
	}
}

func TestCreateSessionReturnsSnapshot(t *testing.T) {
	ts := newTestServer(t)
	var out struct {
		CreateSession struct {
			ID     string
			Tick   int
			Paused bool
			City   struct {
				GridSize int
				Rows     []string
				Hidden   int `json:"hiddenTiles"`
			}
			Cars []struct {
				ID        string
				ClassName string `json:"className"`
				Battery   int
				State     string
			}
		}
	}
	mustQuery(t, ts, `mutation { createSession(id: "t1", seed: 7) {
		id tick paused
		city { gridSize rows hiddenTiles }
		cars { id className battery state }
	} }`, nil, &out)

	s := out.CreateSession
	if s.ID != "t1" {
		t.Errorf("id = %q", s.ID)
	}
	// Sessions start paused: manual driving must not fight a live clock.
	if !s.Paused {
		t.Error("new session should be paused")
	}
	// The map is generated, so assert internal consistency rather than a
	// literal size: the rows returned must match the declared grid.
	if s.City.GridSize < 20 {
		t.Errorf("gridSize = %d, want at least 20", s.City.GridSize)
	}
	if len(s.City.Rows) != s.City.GridSize {
		t.Errorf("rows = %d, want %d", len(s.City.Rows), s.City.GridSize)
	}
	if s.City.Hidden == 0 {
		t.Error("expected hidden tiles in a fresh city")
	}
	if len(s.Cars) != 1 {
		t.Fatalf("cars = %d", len(s.Cars))
	}
	if s.Cars[0].ClassName != "scooter" {
		t.Errorf("className = %q", s.Cars[0].ClassName)
	}
	if s.Cars[0].State != "IDLE" {
		t.Errorf("state = %q, want IDLE", s.Cars[0].State)
	}
}

func TestMoveQueuesAndTickApplies(t *testing.T) {
	ts := newTestServer(t)
	mustQuery(t, ts, `mutation { createSession(id: "m1", seed: 1) { id } }`, nil, nil)

	var before struct {
		Session struct {
			Cars []struct{ X, Y int }
		}
	}
	mustQuery(t, ts, `query { session(id: "m1") { cars { x y } } }`, nil, &before)
	startX, startY := before.Session.Cars[0].X, before.Session.Cars[0].Y

	// Queue only: position must not change.
	var queued struct {
		MoveCar struct {
			Cars []struct {
				X, Y       int
				QueuedMove *string `json:"queuedMove"`
			}
		}
	}
	mustQuery(t, ts, `mutation { moveCar(id: "m1", carId: "car_1", direction: SOUTH) {
		cars { x y queuedMove }
	} }`, nil, &queued)
	c := queued.MoveCar.Cars[0]
	if c.X != startX || c.Y != startY {
		t.Fatalf("car moved on command: (%d,%d) -> (%d,%d)", startX, startY, c.X, c.Y)
	}
	if c.QueuedMove == nil || *c.QueuedMove != "SOUTH" {
		t.Fatalf("queuedMove = %v, want SOUTH", c.QueuedMove)
	}

	// Tick applies it.
	var after struct {
		Tick struct {
			Tick int
			Cars []struct{ X, Y, Battery int }
		}
	}
	mustQuery(t, ts, `mutation { tick(id: "m1", count: 1) { tick cars { x y battery } } }`, nil, &after)
	if after.Tick.Tick != 1 {
		t.Errorf("tick = %d, want 1", after.Tick.Tick)
	}
	if after.Tick.Cars[0].Y != startY+1 {
		t.Errorf("y after tick = %d, want %d", after.Tick.Cars[0].Y, startY+1)
	}
}

func TestFullDeliveryThroughGraphQL(t *testing.T) {
	ts := newTestServer(t)
	mustQuery(t, ts, `mutation { createSession(id: "d1", seed: 5) { id } }`, nil, nil)

	// Force the first order.
	var ticked struct {
		Tick struct {
			Orders []struct {
				ID         string
				State      string
				ItemName   string `json:"itemName"`
				KitchenID  string `json:"kitchenId"`
				CustomerID string `json:"customerId"`
			}
		}
	}
	mustQuery(t, ts, `mutation { tick(id: "d1", count: 1) {
		orders { id state itemName kitchenId customerId }
	} }`, nil, &ticked)
	if len(ticked.Tick.Orders) == 0 {
		t.Fatal("no order spawned")
	}
	o := ticked.Tick.Orders[0]
	if o.ItemName == "" || o.KitchenID == "" || o.CustomerID == "" {
		t.Fatalf("order missing display data: %+v", o)
	}

	var assigned struct {
		AssignOrder struct {
			Orders []struct{ State string }
		}
	}
	mustQuery(t, ts, `mutation { assignOrder(id: "d1", orderId: "`+o.ID+`", carId: "car_1") {
		orders { state }
	} }`, nil, &assigned)

	drive := func(dir string) {
		mustQuery(t, ts, `mutation { moveCar(id: "d1", carId: "car_1", direction: `+dir+`) { tick } }`, nil, nil)
		mustQuery(t, ts, `mutation { tick(id: "d1", count: 1) { tick } }`, nil, nil)
	}

	// Read the city once: rows let the test plan a real route, and the entity
	// lists give the target coordinates. Hardcoding "EAST EAST" would pin this
	// test to one layout, and the map is generated from scripts/genmap.py.
	var view struct {
		Session struct {
			City struct {
				Rows     []string
				Kitchens []struct {
					ID   string
					X, Y int
				}
				Customers []struct {
					ID   string
					X, Y int
				}
			}
			Cars []struct{ X, Y int }
		}
	}
	mustQuery(t, ts, `query { session(id: "d1") { city { rows kitchens { id x y } customers { id x y } } cars { x y } } }`, nil, &view)

	var kx, ky, cx, cy int
	for _, k := range view.Session.City.Kitchens {
		if k.ID == o.KitchenID {
			kx, ky = k.X, k.Y
		}
	}
	for _, c := range view.Session.City.Customers {
		if c.ID == o.CustomerID {
			cx, cy = c.X, c.Y
		}
	}

	// driveTo follows a BFS route across the rendered grid, one queued step at
	// a time, so it goes around buildings exactly as a player would.
	driveTo := func(toX, toY int) {
		from := engine.Position{X: view.Session.Cars[0].X, Y: view.Session.Cars[0].Y}
		path := testRoute(view.Session.City.Rows, from, engine.Position{X: toX, Y: toY})
		if len(path) == 0 {
			t.Fatalf("no route from %v to (%d,%d)", from, toX, toY)
		}
		for _, d := range path {
			drive(strings.ToUpper(string(d)))
		}
		// Keep the local copy of the position in step for the next leg.
		view.Session.Cars[0].X, view.Session.Cars[0].Y = toX, toY
	}

	driveTo(kx, ky)

	var picked struct {
		Session struct {
			Orders []struct {
				State string
				Paid  *int
			}
			Cars []struct {
				Cargo []string
			}
		}
	}
	mustQuery(t, ts, `query { session(id: "d1") { orders { state paid } cars { cargo } } }`, nil, &picked)
	if picked.Session.Orders[0].State != "PICKED_UP" {
		t.Fatalf("state after kitchen = %q, want PICKED_UP", picked.Session.Orders[0].State)
	}
	if len(picked.Session.Cars[0].Cargo) != 1 {
		t.Fatalf("cargo = %v", picked.Session.Cars[0].Cargo)
	}

	driveTo(cx, cy)

	var done struct {
		Session struct {
			Orders []struct {
				ID    string
				State string
				Paid  *int
			}
			Ledger struct {
				Deliveries int
				Money      int
			}
		}
	}
	mustQuery(t, ts, `query { session(id: "d1") {
		orders { id state paid }
		ledger { deliveries money }
	} }`, nil, &done)

	// The order list is newest-first, so index 0 may be a later order. Find the
	// one this test actually drove.
	var state string
	for _, x := range done.Session.Orders {
		if x.ID == o.ID {
			state = x.State
		}
	}
	if state != "DONE" {
		t.Fatalf("final state = %q, want DONE", state)
	}
	if done.Session.Ledger.Deliveries != 1 {
		t.Errorf("deliveries = %d, want 1", done.Session.Ledger.Deliveries)
	}
	if done.Session.Ledger.Money <= 0 {
		t.Errorf("money = %d, want > 0", done.Session.Ledger.Money)
	}
}

func TestCarViewWindow(t *testing.T) {
	ts := newTestServer(t)
	mustQuery(t, ts, `mutation { createSession(id: "w1", seed: 1) { id } }`, nil, nil)

	var out struct {
		CarView struct {
			OriginX, OriginY int
			RowSpan, ColSpan int
			Rows             []string
			Car              struct{ ID string }
			VisibleCars      []struct{ ID string }
		}
	}
	mustQuery(t, ts, `query { carView(id: "w1", carId: "car_1", radius: 2) {
		originX originY rowSpan colSpan rows
		car { id }
		visibleCars { id }
	} }`, nil, &out)

	w := out.CarView
	if w.RowSpan != 5 || w.ColSpan != 5 {
		t.Errorf("span = %dx%d, want 5x5", w.RowSpan, w.ColSpan)
	}
	if len(w.Rows) != w.RowSpan {
		t.Errorf("rows = %d, want %d", len(w.Rows), w.RowSpan)
	}
	for _, r := range w.Rows {
		if len([]rune(r)) != w.ColSpan {
			t.Errorf("row %q has %d cols, want %d", r, len([]rune(r)), w.ColSpan)
		}
	}
	if w.Car.ID != "car_1" {
		t.Errorf("car = %q", w.Car.ID)
	}
	if len(w.VisibleCars) == 0 {
		t.Error("the car itself should be visible in its own window")
	}
}

func TestCarViewRejectsUnknownCar(t *testing.T) {
	ts := newTestServer(t)
	mustQuery(t, ts, `mutation { createSession(id: "w2", seed: 1) { id } }`, nil, nil)

	res := query(t, ts, `query { carView(id: "w2", carId: "nope") { rowSpan } }`, nil)
	if len(res.Errors) == 0 {
		t.Fatal("expected an error for an unknown car")
	}
	if !contains(res.Errors[0].Message, "not_found") {
		t.Errorf("error message = %q, want a not_found code", res.Errors[0].Message)
	}
}

func TestUnknownSessionIsNotFound(t *testing.T) {
	ts := newTestServer(t)
	res := query(t, ts, `query { session(id: "does-not-exist") { id } }`, nil)
	if len(res.Errors) == 0 {
		t.Fatal("expected an error for a missing session")
	}
	if !contains(res.Errors[0].Message, "not_found") {
		t.Errorf("message = %q", res.Errors[0].Message)
	}
}

func TestSessionsListIsNeverNull(t *testing.T) {
	ts := newTestServer(t)
	var out struct {
		Sessions []struct{ ID string }
	}
	mustQuery(t, ts, `query { sessions { id } }`, nil, &out)
	// An empty list must decode as [] rather than erroring on null.
	if out.Sessions == nil {
		t.Fatal("sessions returned null; expected an empty list")
	}
}

func TestBuyCarRejectedWhenLocked(t *testing.T) {
	ts := newTestServer(t)
	mustQuery(t, ts, `mutation { createSession(id: "b1", seed: 1) { id } }`, nil, nil)

	res := query(t, ts, `mutation { buyCar(id: "b1", className: "van") { id } }`, nil)
	if len(res.Errors) == 0 {
		t.Fatal("expected buying a locked class to fail")
	}
	if !contains(res.Errors[0].Message, "conflict") {
		t.Errorf("message = %q, want a conflict code", res.Errors[0].Message)
	}
}

func TestCityQueryRevealsEverything(t *testing.T) {
	ts := newTestServer(t)
	var out struct {
		City struct {
			Hidden int `json:"hiddenTiles"`
			Rows   []string
		}
	}
	mustQuery(t, ts, `query { city { hiddenTiles rows } }`, nil, &out)
	if out.City.Hidden != 0 {
		t.Errorf("hiddenTiles = %d, want 0 for the static preview", out.City.Hidden)
	}
	for _, r := range out.City.Rows {
		if contains(r, "?") {
			t.Fatal("city preview should not contain fog")
		}
	}
}

func TestSpeedValidation(t *testing.T) {
	ts := newTestServer(t)
	mustQuery(t, ts, `mutation { createSession(id: "s1", seed: 1) { id } }`, nil, nil)

	for _, sp := range []int{0, 1, 2, 4} {
		mustQuery(t, ts, `mutation { setSpeed(id: "s1", speed: `+itoa(sp)+`) { speed } }`, nil, nil)
	}
	res := query(t, ts, `mutation { setSpeed(id: "s1", speed: 99) { speed } }`, nil)
	if len(res.Errors) == 0 {
		t.Fatal("speed 99 should be rejected")
	}
}

func TestTickCountIsCapped(t *testing.T) {
	ts := newTestServer(t)
	mustQuery(t, ts, `mutation { createSession(id: "c1", seed: 1) { id } }`, nil, nil)
	res := query(t, ts, `mutation { tick(id: "c1", count: 5000) { tick } }`, nil)
	if len(res.Errors) == 0 {
		t.Fatal("expected an oversized tick count to be rejected")
	}
}

func TestEventsSinceTick(t *testing.T) {
	ts := newTestServer(t)
	mustQuery(t, ts, `mutation { createSession(id: "e1", seed: 1) { id } }`, nil, nil)
	mustQuery(t, ts, `mutation { tick(id: "e1", count: 5) { tick } }`, nil, nil)

	var all struct {
		Events []struct{ Tick int }
	}
	mustQuery(t, ts, `query { events(id: "e1", sinceTick: 0) { tick } }`, nil, &all)
	if len(all.Events) == 0 {
		t.Fatal("expected events after ticking")
	}

	var later struct {
		Events []struct{ Tick int }
	}
	mustQuery(t, ts, `query { events(id: "e1", sinceTick: 4) { tick } }`, nil, &later)
	for _, e := range later.Events {
		if e.Tick < 4 {
			t.Errorf("event at tick %d is older than sinceTick 4", e.Tick)
		}
	}
	if len(later.Events) >= len(all.Events) {
		t.Error("sinceTick should filter out older events")
	}
}

func TestPersistAcrossReload(t *testing.T) {
	ts := newTestServer(t)
	mustQuery(t, ts, `mutation { createSession(id: "p1", seed: 11) { id } }`, nil, nil)
	mustQuery(t, ts, `mutation { tick(id: "p1", count: 10) { tick } }`, nil, nil)
	mustQuery(t, ts, `mutation { saveSession(id: "p1") { tick } }`, nil, nil)
	mustQuery(t, ts, `mutation { evictSession(id: "p1") }`, nil, nil)

	var out struct {
		Session struct{ Tick int }
	}
	mustQuery(t, ts, `query { session(id: "p1") { tick } }`, nil, &out)
	if out.Session.Tick != 10 {
		t.Fatalf("tick after reload = %d, want 10", out.Session.Tick)
	}
}

func TestSchemaIntrospectionWorks(t *testing.T) {
	ts := newTestServer(t)
	var out struct {
		Schema struct {
			QueryType struct{ Name string } `json:"queryType"`
		} `json:"__schema"`
	}
	mustQuery(t, ts, `{ __schema { queryType { name } } }`, nil, &out)
	if out.Schema.QueryType.Name != "Query" {
		t.Fatalf("queryType = %q", out.Schema.QueryType.Name)
	}
}

// --- tiny helpers ----------------------------------------------------------

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && bytes.Contains([]byte(haystack), []byte(needle))
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [8]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}

func TestCarViewClampsAtMapEdge(t *testing.T) {
	ts := newTestServer(t)
	mustQuery(t, ts, `mutation { createSession(id: "edge", seed: 1) { id } }`, nil, nil)

	// The starter car sits at (2,2), so a radius of 5 must be clamped by the
	// grid rather than producing negative origins or short rows.
	var out struct {
		CarView struct {
			OriginX, OriginY int
			RowSpan, ColSpan int
			Rows             []string
		}
	}
	mustQuery(t, ts, `query { carView(id: "edge", carId: "car_1", radius: 5) {
		originX originY rowSpan colSpan rows
	} }`, nil, &out)

	w := out.CarView
	if w.OriginX < 0 || w.OriginY < 0 {
		t.Errorf("origin (%d,%d) must not be negative", w.OriginX, w.OriginY)
	}
	if w.RowSpan != len(w.Rows) {
		t.Errorf("rowSpan %d != rows %d", w.RowSpan, len(w.Rows))
	}
	for i, r := range w.Rows {
		if got := len([]rune(r)); got != w.ColSpan {
			t.Errorf("row %d has %d cols, want %d: %q", i, got, w.ColSpan, r)
		}
	}
}

func TestCarViewRadiusIsClamped(t *testing.T) {
	ts := newTestServer(t)
	mustQuery(t, ts, `mutation { createSession(id: "r", seed: 1) { id } }`, nil, nil)

	var out struct {
		CarView struct{ RowSpan int }
	}
	// A huge radius must not try to render the whole map.
	mustQuery(t, ts, `query { carView(id: "r", carId: "car_1", radius: 99) { rowSpan } }`, nil, &out)
	if out.CarView.RowSpan > 21 {
		t.Errorf("rowSpan = %d; radius should clamp to 10 (max 21 rows)", out.CarView.RowSpan)
	}
}

func TestCarViewRejectsNegativeRadius(t *testing.T) {
	ts := newTestServer(t)
	mustQuery(t, ts, `mutation { createSession(id: "neg", seed: 1) { id } }`, nil, nil)
	res := query(t, ts, `query { carView(id: "neg", carId: "car_1", radius: -1) { rowSpan } }`, nil)
	if len(res.Errors) == 0 {
		t.Fatal("expected a negative radius to be rejected")
	}
}

// TestRejectClosedIsMapped guards the closure reject reason crossing the
// GraphQL boundary. rejectFrom is the only translation point between the
// engine's string reasons and the schema enum, and a missing case silently
// degrades to null -- the client sees "no reason" for a move that was refused.
func TestRejectClosedIsMapped(t *testing.T) {
	// Every engine RejectReason must have an enum member. A missing mapping is
	// invisible: convert.go quietly omits the field, so the client sees a null
	// lastReject and reads it as "the move was legal".
	for _, reason := range []string{
		string(engine.RejectBadDirection),
		string(engine.RejectOffGrid),
		string(engine.RejectBlocked),
		string(engine.RejectNoBattery),
		string(engine.RejectClosed),
		string(engine.RejectOccupied),
	} {
		if _, ok := rejectFrom(reason); !ok {
			t.Errorf("rejectFrom(%q) is unmapped; the enum has a case for it", reason)
		}
	}
}
