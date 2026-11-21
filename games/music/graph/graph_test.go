package graph_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/wricardo/drbrain/games/music/game/score"
	"github.com/wricardo/drbrain/games/music/game/service"
	"github.com/wricardo/drbrain/games/music/game/session"
	musicgraph "github.com/wricardo/drbrain/games/music/graph"
	"github.com/wricardo/drbrain/games/music/transport/websocket"
	rootgraph "github.com/wricardo/drbrain/graph"
)

const sessionFields = `id displayName difficulty solved seq wins percentPerWin createdAt lastActionAt
	excerpt { id measures { id } notes { at dur pitch } }
	slots { position measureID flipH flipV }
	lastChange { kind a b }`

type slotJSON struct {
	Position  int    `json:"position"`
	MeasureID string `json:"measureID"`
	FlipH     bool   `json:"flipH"`
	FlipV     bool   `json:"flipV"`
}

type changeJSON struct {
	Kind string `json:"kind"`
	A    int    `json:"a"`
	B    int    `json:"b"`
}

type sessionJSON struct {
	ID            string  `json:"id"`
	DisplayName   *string `json:"displayName"`
	Difficulty    string  `json:"difficulty"`
	Solved        bool    `json:"solved"`
	Seq           int     `json:"seq"`
	Wins          int     `json:"wins"`
	PercentPerWin int     `json:"percentPerWin"`
	CreatedAt     string  `json:"createdAt"`
	LastActionAt  string  `json:"lastActionAt"`
	Excerpt       *struct {
		ID       string `json:"id"`
		Measures []struct {
			ID string `json:"id"`
		} `json:"measures"`
		Notes []struct {
			At    int `json:"at"`
			Dur   int `json:"dur"`
			Pitch int `json:"pitch"`
		} `json:"notes"`
	} `json:"excerpt"`
	Slots      []slotJSON  `json:"slots"`
	LastChange *changeJSON `json:"lastChange"`
}

type gqlError struct {
	Message    string         `json:"message"`
	Extensions map[string]any `json:"extensions"`
}

// fixturePiece is six distinct bars inside the pitch range, the Music Region
// cap, so it passes the same catalogue validation a shipped score does.
func fixturePiece() *score.Piece {
	measures := make([]score.Measure, score.MaxMeasures)
	for i := range measures {
		measures[i] = score.Measure{
			Len: 480,
			Notes: []score.Note{
				{Start: 0, Dur: 120, Pitch: 60 + i},
				{Start: 240, Dur: 120, Pitch: 72 + i%5},
			},
		}
	}
	return &score.Piece{
		Version: score.Version, ID: "scale", Title: "Scale", Composer: "Anonymous",
		TempoBPM: 80, TimeSig: "4/4", Key: "C major", Clef: score.ClefTreble,
		Measures: measures,
	}
}

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	scoresDir := t.TempDir()
	raw, err := score.Encode(fixturePiece())
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if err := os.WriteFile(filepath.Join(scoresDir, "scale.json"), raw, 0o644); err != nil {
		t.Fatalf("write score: %v", err)
	}
	catalogue, err := score.NewCatalogue(scoresDir, t.TempDir())
	if err != nil {
		t.Fatalf("NewCatalogue: %v", err)
	}
	persistence, err := session.NewStorePersistence(t.TempDir(), 100)
	if err != nil {
		t.Fatalf("NewStorePersistence: %v", err)
	}
	manager := session.NewManager(persistence, 100)
	if err := manager.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	hub := websocket.NewHub()
	svc := service.New(catalogue, manager, hub, service.Options{})
	resolver := musicgraph.NewResolver(svc, hub)
	handler := rootgraph.NewHandler(rootgraph.Options{
		Resolver:      rootgraph.New(nil, nil, nil, nil, nil, resolver),
		Introspection: true,
	})
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts
}

func post(t *testing.T, ts *httptest.Server, query string, vars map[string]any) json.RawMessage {
	t.Helper()
	body, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	resp, err := http.Post(ts.URL+"/graphql", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /graphql: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var out struct {
		Data   json.RawMessage `json:"data"`
		Errors []gqlError      `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(out.Errors) != 0 {
		t.Fatalf("graphql errors: %+v", out.Errors)
	}
	return out.Data
}

// operation decodes data.music[field] into v.
func operation(t *testing.T, data json.RawMessage, field string, v any) {
	t.Helper()
	var envelope struct {
		Music map[string]json.RawMessage `json:"music"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatalf("decode data: %v", err)
	}
	raw, ok := envelope.Music[field]
	if !ok {
		t.Fatalf("response has no music.%s: %s", field, data)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatalf("decode music.%s: %v", field, err)
	}
}

func mustSession(t *testing.T, data json.RawMessage, field string) sessionJSON {
	t.Helper()
	var s sessionJSON
	operation(t, data, field, &s)
	return s
}

func TestMusicFlow_CreateSwitchTransformSolve(t *testing.T) {
	ts := newTestServer(t)

	// Catalogue: one excerpt with measures and the flattened target stream.
	var excerpts []struct {
		ID       string `json:"id"`
		Title    string `json:"title"`
		TempoBpm int    `json:"tempoBPM"`
		Measures []struct {
			ID    string `json:"id"`
			Len   int    `json:"len"`
			Notes []struct {
				At    int `json:"at"`
				Pitch int `json:"pitch"`
			} `json:"notes"`
		} `json:"measures"`
		Notes []struct {
			At    int `json:"at"`
			Pitch int `json:"pitch"`
		} `json:"notes"`
	}
	operation(t, post(t, ts, `{ music { excerpts { id title tempoBPM measures { id len notes { at pitch } } notes { at pitch } } } }`, nil), "excerpts", &excerpts)
	if len(excerpts) != 1 || excerpts[0].ID != "scale" {
		t.Fatalf("excerpts = %+v", excerpts)
	}
	if len(excerpts[0].Measures) != score.MaxMeasures || len(excerpts[0].Notes) != 2*score.MaxMeasures {
		t.Fatalf("measures/notes = %d/%d, want %d/%d", len(excerpts[0].Measures), len(excerpts[0].Notes), score.MaxMeasures, 2*score.MaxMeasures)
	}

	// Create an Expert session: all six bars, horizontal flips only.
	created := mustSession(t, post(t, ts,
		`mutation($id: ID!, $d: Music_Difficulty!, $seed: Int) { music { createSession(excerptID: $id, difficulty: $d, displayName: "Ada", seed: $seed) { `+sessionFields+` } } }`,
		map[string]any{"id": "scale", "d": "EXPERT", "seed": 7}), "createSession")
	if created.ID == "" || created.Solved || len(created.Slots) != score.MaxMeasures {
		t.Fatalf("created = %+v", created)
	}
	if created.Difficulty != "EXPERT" || created.PercentPerWin != 10 {
		t.Fatalf("difficulty/percent = %q/%d", created.Difficulty, created.PercentPerWin)
	}
	if created.DisplayName == nil || *created.DisplayName != "Ada" {
		t.Fatalf("display name = %v", created.DisplayName)
	}
	if created.CreatedAt == "" || created.LastActionAt == "" {
		t.Fatal("timestamps are empty")
	}

	// Swap two bars.
	switched := mustSession(t, post(t, ts,
		`mutation($id: ID!, $a: Int!, $b: Int!) { music { switchMeasures(sessionID: $id, a: $a, b: $b) { `+sessionFields+` } } }`,
		map[string]any{"id": created.ID, "a": 0, "b": 3}), "switchMeasures")
	if switched.Seq != created.Seq+1 {
		t.Fatalf("seq = %d, want %d", switched.Seq, created.Seq+1)
	}
	if switched.Slots[0].MeasureID != created.Slots[3].MeasureID || switched.Slots[3].MeasureID != created.Slots[0].MeasureID {
		t.Fatalf("bars not swapped: %+v", switched.Slots)
	}
	if switched.LastChange == nil || switched.LastChange.Kind != service.KindSwitch {
		t.Fatalf("last change = %+v", switched.LastChange)
	}

	// Flip one bar both ways.
	transformed := mustSession(t, post(t, ts,
		`mutation($id: ID!, $p: Int!, $h: Boolean!, $v: Boolean!) { music { transformMeasure(sessionID: $id, position: $p, flipH: $h, flipV: $v) { `+sessionFields+` } } }`,
		map[string]any{"id": created.ID, "p": 2, "h": true, "v": true}), "transformMeasure")
	if !transformed.Slots[2].FlipH || !transformed.Slots[2].FlipV {
		t.Fatalf("slot 2 = %+v", transformed.Slots[2])
	}
	if transformed.LastChange == nil || transformed.LastChange.Kind != service.KindTransform || transformed.LastChange.A != 2 {
		t.Fatalf("last change = %+v", transformed.LastChange)
	}

	// Solve it: m001..m006 in order, no flips.
	slots := make([]map[string]any, score.MaxMeasures)
	for i := range slots {
		slots[i] = map[string]any{"measureID": fmt.Sprintf("m%03d", i+1), "flipH": false, "flipV": false}
	}
	solved := mustSession(t, post(t, ts,
		`mutation($id: ID!, $slots: [Music_PlacementInput!]!) { music { setArrangement(sessionID: $id, slots: $slots) { `+sessionFields+` } } }`,
		map[string]any{"id": created.ID, "slots": slots}), "setArrangement")
	if !solved.Solved || solved.Wins != 1 {
		t.Fatalf("solved = %v, wins = %d", solved.Solved, solved.Wins)
	}
	if solved.LastChange == nil || solved.LastChange.Kind != service.KindSolve {
		t.Fatalf("last change = %+v", solved.LastChange)
	}

	// Query it back, then reset to the scramble.
	queried := mustSession(t, post(t, ts,
		`query($id: ID!) { music { session(id: $id) { `+sessionFields+` } } }`,
		map[string]any{"id": created.ID}), "session")
	if !queried.Solved || queried.Wins != 1 || queried.Seq != solved.Seq {
		t.Fatalf("queried = %+v", queried)
	}

	reset := mustSession(t, post(t, ts,
		`mutation($id: ID!) { music { resetArrangement(sessionID: $id) { `+sessionFields+` } } }`,
		map[string]any{"id": created.ID}), "resetArrangement")
	if reset.Solved || reset.LastChange == nil || reset.LastChange.Kind != service.KindReset {
		t.Fatalf("reset = %+v", reset)
	}

	// Delete removes it; the session query then returns null.
	var deleted bool
	operation(t, post(t, ts,
		`mutation($id: ID!) { music { deleteSession(id: $id) } }`,
		map[string]any{"id": created.ID}), "deleteSession", &deleted)
	if !deleted {
		t.Fatal("deleteSession = false")
	}
	var gone *sessionJSON
	operation(t, post(t, ts,
		`query($id: ID!) { music { session(id: $id) { id } } }`,
		map[string]any{"id": created.ID}), "session", &gone)
	if gone != nil {
		t.Fatalf("deleted session = %+v", gone)
	}
}

func TestMusicFlow_InvalidArrangementReturnsCode(t *testing.T) {
	ts := newTestServer(t)
	created := mustSession(t, post(t, ts,
		`mutation($id: ID!) { music { createSession(excerptID: $id, difficulty: NOVICE, seed: 3) { `+sessionFields+` } } }`,
		map[string]any{"id": "scale"}), "createSession")

	slots := make([]map[string]any, len(created.Slots))
	for i := range slots {
		slots[i] = map[string]any{"measureID": "m999", "flipH": false, "flipV": false}
	}
	body, err := json.Marshal(map[string]any{
		"query": `mutation($id: ID!, $slots: [Music_PlacementInput!]!) { music { setArrangement(sessionID: $id, slots: $slots) { id } } }`,
		"variables": map[string]any{
			"id": created.ID, "slots": slots,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(ts.URL+"/graphql", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	var out struct {
		Errors []gqlError `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Errors) != 1 {
		t.Fatalf("errors = %+v", out.Errors)
	}
	if code, _ := out.Errors[0].Extensions["code"].(string); code != service.CodeInvalidArgument {
		t.Fatalf("code = %v, want %s", out.Errors[0].Extensions["code"], service.CodeInvalidArgument)
	}
}

func TestMusicFlow_ExcerptUnknownReturnsNull(t *testing.T) {
	ts := newTestServer(t)
	var excerpt *struct {
		ID string `json:"id"`
	}
	operation(t, post(t, ts, `{ music { excerpt(id: "nope") { id } } }`, nil), "excerpt", &excerpt)
	if excerpt != nil {
		t.Fatalf("excerpt = %+v, want null", excerpt)
	}
}
