package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/wricardo/drbrain-motor-programming/game/config"
	"github.com/wricardo/drbrain-motor-programming/game/service"
	"github.com/wricardo/drbrain-motor-programming/game/session"
	"github.com/wricardo/drbrain-motor-programming/transport/websocket"
)

// The frontend's leaderboard query (frontend/src/lib/queries.ts), kept here so
// the complexity test fails when the page outgrows the cap.
const leaderboardQ = `query Leaderboard($mapId: ID!, $order: LeaderboardOrder, $limit: Int, $sessionId: ID) { leaderboard(mapId: $mapId, order: $order, limit: $limit, sessionId: $sessionId) { mapId order total entries { rank sessionId displayName handle mine slots steps achievedAt } yours { rank sessionId displayName handle mine slots steps achievedAt } } }`

// The frontend's my-runs queries (frontend/src/lib/queries.ts).
const playerRunsQ = `query PlayerRuns($sessionIds: [ID!], $name: String) { playerRuns(sessionIds: $sessionIds, name: $name) { mapId total entry { rank sessionId displayName handle mine slots steps achievedAt } } }`

// newRealServer serves the real game service over a one-map temp directory.
func newRealServer(t *testing.T) *httptest.Server {
	t.Helper()
	dir := t.TempDir()
	// 8x3, 7 forward moves to the treat; main 8 slots, one sub of 4.
	const wide = `{"id":"wide","name":"Wide","layout":["........",">......*","........"],"main_tape_length":8,"sub_tape_lengths":[4],"max_steps":40,"max_call_depth":8}`
	if err := os.WriteFile(filepath.Join(dir, "wide.json"), []byte(wide), 0o600); err != nil {
		t.Fatal(err)
	}
	maps, err := config.NewManager(config.Options{Dir: dir, UserDir: filepath.Join(t.TempDir(), "user")})
	if err != nil {
		t.Fatal(err)
	}
	hub := websocket.NewHub()
	svc := service.New(maps, session.NewManager(nil, 0), hub, service.Options{})
	t.Cleanup(svc.Shutdown)
	s, err := NewServer(svc, hub, Options{UIDirs: []string{filepath.Join(t.TempDir(), "missing")}})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)
	return ts
}

func dig(t *testing.T, v any, path ...string) any {
	t.Helper()
	for _, p := range path {
		m, ok := v.(map[string]any)
		if !ok {
			t.Fatalf("at %q: %T is not an object", p, v)
		}
		v = m[p]
	}
	return v
}

// TestLeaderboardOverHTTP plays two sessions through the public API only and
// reads the board back, the way an agent would.
func TestLeaderboardOverHTTP(t *testing.T) {
	ts := newRealServer(t)

	createWith := func(name, main, sub string) string {
		_, out := gql(t, ts.URL, `mutation { createSession(mapID: "wide", displayName: "`+name+`") { id } }`, nil)
		id, _ := dig(t, out, "data", "createSession", "id").(string)
		if id == "" {
			t.Fatalf("createSession: %v", out)
		}
		if _, out = gql(t, ts.URL, `mutation { setProgram(sessionID: "`+id+`", program: {main: [`+main+`], subs: [[`+sub+`]]}) { id } }`, nil); out["errors"] != nil {
			t.Fatalf("setProgram: %v", out)
		}
		for range 30 {
			_, out = gql(t, ts.URL, `mutation { step(sessionID: "`+id+`") { vm { status } } }`, nil)
			if out["errors"] != nil {
				t.Fatalf("step: %v", out)
			}
			if dig(t, out, "data", "step", "vm", "status") == "WON" {
				return id
			}
		}
		t.Fatal("program never won")
		return ""
	}
	mv := func(n int) string { return strings.TrimSuffix(strings.Repeat("MOVE_FORWARD,", n), ",") }
	pad := func(list string, n int) string { // fill a tape of n slots with EMPTY
		have := 0
		if list != "" {
			have = strings.Count(list, ",") + 1
		}
		parts := []string{}
		if list != "" {
			parts = append(parts, list)
		}
		for ; have < n; have++ {
			parts = append(parts, "EMPTY")
		}
		return strings.Join(parts, ",")
	}

	direct := createWith("Direct", pad(mv(7), 8), pad("", 4))                             // 7 slots, 7 steps
	via := createWith("Via", pad("CALL_SUB_1,CALL_SUB_1,MOVE_FORWARD", 8), pad(mv(3), 4)) // 6 slots, 9 steps
	vars := func(order string) map[string]any {
		return map[string]any{"mapId": "wide", "order": order, "sessionId": direct}
	}

	run := func(order string) map[string]any {
		t.Helper()
		_, out := gqlVars(t, ts.URL, leaderboardQ, vars(order))
		if out["errors"] != nil {
			t.Fatalf("leaderboard(%s): %v", order, out["errors"])
		}
		return dig(t, out, "data", "leaderboard").(map[string]any)
	}
	names := func(lb map[string]any) []string {
		var out []string
		for _, e := range lb["entries"].([]any) {
			out = append(out, e.(map[string]any)["displayName"].(string))
		}
		return out
	}

	slots := run("SLOTS")
	if got := strings.Join(names(slots), ","); got != "Via,Direct" {
		t.Fatalf("SLOTS order = %s, want Via,Direct (fewest slots first)", got)
	}
	if slots["total"] != float64(2) || slots["order"] != "SLOTS" {
		t.Fatalf("board header = %v", slots)
	}
	first := slots["entries"].([]any)[0].(map[string]any)
	if first["sessionId"] != via || first["slots"] != float64(6) || first["steps"] != float64(9) || first["rank"] != float64(1) {
		t.Fatalf("first entry = %v", first)
	}
	if yours := slots["yours"].(map[string]any); yours["sessionId"] != direct || yours["rank"] != float64(2) {
		t.Fatalf("yours = %v", yours)
	}

	if got := strings.Join(names(run("STEPS")), ","); got != "Direct,Via" {
		t.Fatalf("STEPS order = %s, want Direct,Via (fewest steps first)", got)
	}

	t.Run("a missing map is null", func(t *testing.T) {
		_, out := gql(t, ts.URL, `{ leaderboard(mapId: "nope") { total } }`, nil)
		if out["errors"] != nil || dig(t, out, "data", "leaderboard") != nil {
			t.Fatalf("out = %v, want data.leaderboard null and no errors", out)
		}
	})
	t.Run("bad arguments are INVALID_ARGUMENT", func(t *testing.T) {
		_, out := gql(t, ts.URL, `{ leaderboard(mapId: "wide", limit: -3) { total } }`, nil)
		if code := firstErrCode(out); code != service.CodeInvalidArgument {
			t.Fatalf("code = %q, want %s (%v)", code, service.CodeInvalidArgument, out)
		}
	})
	t.Run("an unknown order is rejected by the schema", func(t *testing.T) {
		_, out := gql(t, ts.URL, `{ leaderboard(mapId: "wide", order: FASTEST) { total } }`, nil)
		if out["errors"] == nil {
			t.Fatalf("out = %v, want a validation error", out)
		}
	})
	t.Run("only public result fields exist", func(t *testing.T) {
		// A board entry has no program, so a winning route cannot be read from it.
		_, out := gql(t, ts.URL, `{ leaderboard(mapId: "wide") { entries { program } } }`, nil)
		if out["errors"] == nil {
			t.Fatalf("out = %v, want a validation error for an unknown field", out)
		}
	})
}

// gqlVars is gql with variables.
func gqlVars(t *testing.T, url, query string, vars map[string]any) (int, map[string]any) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"query": query, "variables": vars})
	resp, err := http.Post(url+"/graphql", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	raw, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

// TestGuestNamesAndPlayerRunsOverHTTP: a session created without a name gets a
// guest name, and the player's runs can be read back by session id or by name.
func TestGuestNamesAndPlayerRunsOverHTTP(t *testing.T) {
	ts := newRealServer(t)

	_, out := gql(t, ts.URL, `mutation { createSession(mapID: "wide") { id displayName } }`, nil)
	id, _ := dig(t, out, "data", "createSession", "id").(string)
	guest, _ := dig(t, out, "data", "createSession", "displayName").(string)
	if !regexp.MustCompile(`^[A-Z][a-z]+[A-Z][a-z]+$`).MatchString(guest) {
		t.Fatalf("displayName = %q, want a <Adjective><Noun> guest name (%v)", guest, out)
	}
	main := "[" + strings.Repeat("MOVE_FORWARD,", 7) + "EMPTY]"
	if _, out = gql(t, ts.URL, `mutation { setProgram(sessionID: "`+id+`", program: {main: `+main+`, subs: [[EMPTY,EMPTY,EMPTY,EMPTY]]}) { id } }`, nil); out["errors"] != nil {
		t.Fatalf("setProgram: %v", out)
	}
	for range 7 {
		gql(t, ts.URL, `mutation { step(sessionID: "`+id+`") { id } }`, nil)
	}

	check := func(label string, vars map[string]any) {
		t.Helper()
		_, out := gqlVars(t, ts.URL, playerRunsQ, vars)
		if out["errors"] != nil {
			t.Fatalf("%s: %v", label, out["errors"])
		}
		runs, _ := dig(t, out, "data", "playerRuns").([]any)
		if len(runs) != 1 {
			t.Fatalf("%s: runs = %v, want one", label, runs)
		}
		run := runs[0].(map[string]any)
		entry := run["entry"].(map[string]any)
		if run["mapId"] != "wide" || run["total"] != float64(1) || entry["rank"] != float64(1) ||
			entry["sessionId"] != id || entry["displayName"] != guest || entry["slots"] != float64(7) || entry["steps"] != float64(7) {
			t.Fatalf("%s: run = %v", label, run)
		}
	}
	check("by session ids", map[string]any{"sessionIds": []string{id, "0123456789abcdef"}})
	check("by name", map[string]any{"name": strings.ToLower(guest)})

	t.Run("nothing won is an empty list", func(t *testing.T) {
		_, out := gqlVars(t, ts.URL, playerRunsQ, map[string]any{"name": "NobodyAtAll"})
		if runs, ok := dig(t, out, "data", "playerRuns").([]any); !ok || len(runs) != 0 {
			t.Fatalf("out = %v, want []", out)
		}
	})
	t.Run("the selector must be exactly one", func(t *testing.T) {
		for label, vars := range map[string]map[string]any{
			"neither": {},
			"both":    {"name": "x", "sessionIds": []string{id}},
		} {
			_, out := gqlVars(t, ts.URL, playerRunsQ, vars)
			if code := firstErrCode(out); code != service.CodeInvalidArgument {
				t.Fatalf("%s: code = %q (%v)", label, code, out)
			}
		}
	})
}
