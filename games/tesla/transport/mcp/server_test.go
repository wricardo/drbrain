package mcp

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/wricardo/drbrain/games/tesla/appconfig"
	"github.com/wricardo/drbrain/games/tesla/game/engine"
	"github.com/wricardo/drbrain/games/tesla/game/service"
	"github.com/wricardo/drbrain/games/tesla/transport/websocket"
)

type mockGameService struct {
	mapEntry          *service.MapEntry
	updatedActor      service.MapActor
	updatedName       string
	deletedName       string
	deletedActor      service.MapActor
	createMapCalls    int
	moveFunc          func(ctx context.Context, sessionID, direction string, reset bool) (*service.MoveResult, error)
	bulkMoveFunc      func(ctx context.Context, sessionID string, moves []string, reset bool) (*service.BulkMoveResult, error)
	resetFunc         func(ctx context.Context, sessionID string) (*engine.GameState, error)
	createSessionFunc func(ctx context.Context, configName string, opts ...service.CreateSessionOptions) (*service.SessionInfo, error)
	historyFunc       func(ctx context.Context, sessionID string, opts service.HistoryOptions) (*service.HistoryResponse, error)
	listSessionsFunc  func(ctx context.Context) ([]*service.SessionInfo, error)
}

func (m *mockGameService) CreateSession(ctx context.Context, configName string, opts ...service.CreateSessionOptions) (*service.SessionInfo, error) {
	if m.createSessionFunc != nil {
		return m.createSessionFunc(ctx, configName, opts...)
	}
	return &service.SessionInfo{}, nil
}
func (m *mockGameService) GetSession(ctx context.Context, sessionID string) (*service.SessionInfo, error) {
	return &service.SessionInfo{}, nil
}
func (m *mockGameService) ListSessions(ctx context.Context) ([]*service.SessionInfo, error) {
	if m.listSessionsFunc != nil {
		return m.listSessionsFunc(ctx)
	}
	return []*service.SessionInfo{}, nil
}
func (m *mockGameService) DeleteSession(ctx context.Context, sessionID string) error { return nil }

// Map lifecycle: the MCP tests exercise sessions only, so these are inert.
func (m *mockGameService) ListMapEntries(context.Context, string) ([]*service.MapEntry, error) {
	return nil, nil
}
func (m *mockGameService) GetMapEntry(context.Context, string) (*service.MapEntry, error) {
	if m.mapEntry != nil {
		return m.mapEntry, nil
	}
	return nil, service.ErrMapNotFound
}
func (m *mockGameService) CreateMapDraft(context.Context, string, *engine.GameConfig, string) (*service.MapEntry, string, error) {
	return nil, "", service.ErrMapInvalid
}
func (m *mockGameService) UpdateMapOwned(_ context.Context, name string, cfg *engine.GameConfig, actor service.MapActor) (*service.MapEntry, error) {
	m.updatedName = name
	m.updatedActor = actor
	return &service.MapEntry{Name: name, Config: cfg, Status: service.MapStatusPublished}, nil
}
func (m *mockGameService) PublishMap(context.Context, string, service.MapActor) (*service.MapEntry, error) {
	return nil, service.ErrMapNotFound
}
func (m *mockGameService) DeleteMapOwned(_ context.Context, name string, actor service.MapActor) error {
	m.deletedName = name
	m.deletedActor = actor
	return nil
}
func (m *mockGameService) ClaimMap(context.Context, string, string, string) (*service.MapEntry, error) {
	return nil, service.ErrMapNotFound
}
func (m *mockGameService) UpdateSessionDisplayName(ctx context.Context, sessionID, displayName string) (*service.SessionInfo, error) {
	return &service.SessionInfo{}, nil
}
func (m *mockGameService) Move(ctx context.Context, sessionID, direction string, reset bool) (*service.MoveResult, error) {
	if m.moveFunc != nil {
		return m.moveFunc(ctx, sessionID, direction, reset)
	}
	return &service.MoveResult{Success: true, GameState: &engine.GameState{}}, nil
}
func (m *mockGameService) BulkMove(ctx context.Context, sessionID string, moves []string, reset bool, _ service.BulkMoveOptions) (*service.BulkMoveResult, error) {
	if m.bulkMoveFunc != nil {
		return m.bulkMoveFunc(ctx, sessionID, moves, reset)
	}
	return &service.BulkMoveResult{Success: true, GameState: &engine.GameState{}}, nil
}
func (m *mockGameService) Reset(ctx context.Context, sessionID string) (*engine.GameState, error) {
	if m.resetFunc != nil {
		return m.resetFunc(ctx, sessionID)
	}
	return &engine.GameState{}, nil
}
func (m *mockGameService) GetGameState(ctx context.Context, sessionID string) (*engine.GameState, error) {
	return &engine.GameState{}, nil
}
func (m *mockGameService) GetMoveHistory(ctx context.Context, sessionID string, opts service.HistoryOptions) (*service.HistoryResponse, error) {
	if m.historyFunc != nil {
		return m.historyFunc(ctx, sessionID, opts)
	}
	return &service.HistoryResponse{}, nil
}
func (m *mockGameService) ListMaps(ctx context.Context) ([]*service.MapInfo, error) {
	return []*service.MapInfo{}, nil
}
func (m *mockGameService) LoadMap(ctx context.Context, mapName string) (*engine.GameConfig, error) {
	return &engine.GameConfig{}, nil
}
func (m *mockGameService) SaveMap(ctx context.Context, mapName string, config *engine.GameConfig) error {
	return nil
}
func (m *mockGameService) CreateMap(ctx context.Context, mapName string, config *engine.GameConfig) (*service.MapCreateResult, error) {
	return &service.MapCreateResult{Map: config, EditKey: "test-edit-key"}, nil
}
func (m *mockGameService) UpdateMap(ctx context.Context, mapName string, config *engine.GameConfig, allowBuiltin bool) error {
	return nil
}
func (m *mockGameService) MapAuthorization(ctx context.Context, mapName string) (bool, bool, error) {
	return true, true, nil
}
func (m *mockGameService) VerifyMapEditToken(ctx context.Context, mapName, editKey string) error {
	return nil
}
func (m *mockGameService) DeleteMap(ctx context.Context, mapName string, allowBuiltin bool) error {
	return nil
}

func expectState(t *testing.T, ch <-chan *websocket.StateUpdate, want *engine.GameState) {
	t.Helper()
	select {
	case got := <-ch:
		if !reflect.DeepEqual(got.State, want) {
			t.Fatalf("unexpected broadcast state: got %+v want %+v", got, want)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for broadcast state")
	}
}

func TestHandleMoveBroadcastsToSessionAndLobby(t *testing.T) {
	sessionID := "session-1"
	want := &engine.GameState{Battery: 42, Score: 3, PlayerPos: engine.Position{X: 2, Y: 1}}
	mockSvc := &mockGameService{
		moveFunc: func(ctx context.Context, gotSessionID, direction string, reset bool) (*service.MoveResult, error) {
			if gotSessionID != sessionID {
				t.Fatalf("unexpected session ID: got %q", gotSessionID)
			}
			return &service.MoveResult{Success: true, GameState: want}, nil
		},
	}
	hub := websocket.NewHub()
	server := NewServer(mockSvc, hub)

	sessionCtx, cancelSession := context.WithCancel(context.Background())
	defer cancelSession()
	lobbyCtx, cancelLobby := context.WithCancel(context.Background())
	defer cancelLobby()
	sessionSub := hub.SubscribeSession(sessionCtx, sessionID)
	lobbySub := hub.SubscribeLobby(lobbyCtx)

	_, err := server.handleMove(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Arguments: map[string]interface{}{
				"session_id": sessionID,
				"direction":  "right",
			},
		},
	})
	if err != nil {
		t.Fatalf("handleMove returned error: %v", err)
	}

	expectState(t, sessionSub, want)
	expectState(t, lobbySub, want)
}

func TestHandleBulkMoveBroadcastsToSession(t *testing.T) {
	sessionID := "session-2"
	want := &engine.GameState{Battery: 30, Score: 7, PlayerPos: engine.Position{X: 4, Y: 5}}
	mockSvc := &mockGameService{
		bulkMoveFunc: func(ctx context.Context, gotSessionID string, moves []string, reset bool) (*service.BulkMoveResult, error) {
			if gotSessionID != sessionID {
				t.Fatalf("unexpected session ID: got %q", gotSessionID)
			}
			return &service.BulkMoveResult{Success: true, GameState: want}, nil
		},
	}
	hub := websocket.NewHub()
	server := NewServer(mockSvc, hub)

	sessionCtx, cancelSession := context.WithCancel(context.Background())
	defer cancelSession()
	sessionSub := hub.SubscribeSession(sessionCtx, sessionID)

	_, err := server.handleBulkMove(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Arguments: map[string]interface{}{
				"session_id": sessionID,
				"moves":      []interface{}{"up", "left"},
			},
		},
	})
	if err != nil {
		t.Fatalf("handleBulkMove returned error: %v", err)
	}

	expectState(t, sessionSub, want)
}

func TestHandleResetBroadcastsToSession(t *testing.T) {
	sessionID := "session-3"
	want := &engine.GameState{Battery: 50, MaxBattery: 50, PlayerPos: engine.Position{X: 0, Y: 0}}
	mockSvc := &mockGameService{
		resetFunc: func(ctx context.Context, gotSessionID string) (*engine.GameState, error) {
			if gotSessionID != sessionID {
				t.Fatalf("unexpected session ID: got %q", gotSessionID)
			}
			return want, nil
		},
	}
	hub := websocket.NewHub()
	server := NewServer(mockSvc, hub)

	sessionCtx, cancelSession := context.WithCancel(context.Background())
	defer cancelSession()
	sessionSub := hub.SubscribeSession(sessionCtx, sessionID)

	_, err := server.handleReset(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Arguments: map[string]interface{}{
				"session_id": sessionID,
			},
		},
	})
	if err != nil {
		t.Fatalf("handleReset returned error: %v", err)
	}

	expectState(t, sessionSub, want)
}

func TestHandleMoveWithoutHubDoesNotPanic(t *testing.T) {
	mockSvc := &mockGameService{
		moveFunc: func(ctx context.Context, gotSessionID, direction string, reset bool) (*service.MoveResult, error) {
			return &service.MoveResult{Success: true, GameState: &engine.GameState{Battery: 1}}, nil
		},
	}
	server := NewServer(mockSvc, nil)

	if _, err := server.handleMove(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Arguments: map[string]interface{}{
				"session_id": "session-nil-hub",
				"direction":  "up",
			},
		},
	}); err != nil {
		t.Fatalf("handleMove returned error: %v", err)
	}
}

func TestFilterGameState_DefaultDropsHistory(t *testing.T) {
	state := &engine.GameState{
		Grid: [][]engine.Cell{{{Type: engine.Road}}},
		MoveHistory: []engine.MoveHistoryEntry{
			{MoveNumber: 1, Action: "left"},
			{MoveNumber: 2, Action: "right"},
		},
		CurrentMoves: []engine.MoveHistoryEntry{
			{MoveNumber: 2, Action: "right"},
		},
		CurrentMovesCount: 1,
	}
	opts := &service.ResponseOptions{
		IncludeGrid: false,
	}

	filtered := filterGameState(state, opts)

	if filtered == nil {
		t.Fatal("expected filtered state")
	}
	if filtered.Grid != nil {
		t.Fatalf("expected grid to be omitted by default")
	}
	if filtered.MoveHistory != nil {
		t.Fatalf("expected move history to be omitted by default")
	}
	if filtered.CurrentMoves != nil {
		t.Fatalf("expected current moves to be omitted by default")
	}
	if filtered.CurrentMovesCount != 0 {
		t.Fatalf("expected current moves count to reset to 0, got %d", filtered.CurrentMovesCount)
	}
}

func TestFilterGameState_HistoryLimitApplied(t *testing.T) {
	state := &engine.GameState{
		MoveHistory: []engine.MoveHistoryEntry{
			{MoveNumber: 1, Action: "a1"},
			{MoveNumber: 2, Action: "a2"},
			{MoveNumber: 3, Action: "a3"},
		},
		CurrentMoves: []engine.MoveHistoryEntry{
			{MoveNumber: 10, Action: "c1"},
			{MoveNumber: 11, Action: "c2"},
			{MoveNumber: 12, Action: "c3"},
		},
		CurrentMovesCount: 3,
	}
	opts := &service.ResponseOptions{
		IncludeGrid:    true,
		IncludeHistory: true,
		HistoryLimit:   2,
	}

	filtered := filterGameState(state, opts)

	if filtered == nil {
		t.Fatal("expected filtered state")
	}
	if len(filtered.MoveHistory) != 2 || filtered.MoveHistory[0].MoveNumber != 2 || filtered.MoveHistory[1].MoveNumber != 3 {
		t.Fatalf("expected last 2 move history entries, got %+v", filtered.MoveHistory)
	}
	if len(filtered.CurrentMoves) != 2 || filtered.CurrentMoves[0].MoveNumber != 11 || filtered.CurrentMoves[1].MoveNumber != 12 {
		t.Fatalf("expected last 2 current move entries, got %+v", filtered.CurrentMoves)
	}
	if filtered.CurrentMovesCount != 2 {
		t.Fatalf("expected current move count 2, got %d", filtered.CurrentMovesCount)
	}
}

func TestHandleMove_DefaultOutputOmitsHistoryFields(t *testing.T) {
	mockSvc := &mockGameService{
		moveFunc: func(ctx context.Context, gotSessionID, direction string, reset bool) (*service.MoveResult, error) {
			return &service.MoveResult{
				Success: true,
				GameState: &engine.GameState{
					MoveHistory: []engine.MoveHistoryEntry{{MoveNumber: 1}},
					CurrentMoves: []engine.MoveHistoryEntry{
						{MoveNumber: 1},
					},
					CurrentMovesCount: 1,
				},
			}, nil
		},
	}
	server := NewServer(mockSvc, nil)

	result, err := server.handleMove(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Arguments: map[string]interface{}{
				"session_id": "session-default-omit",
				"direction":  "left",
			},
		},
	})
	if err != nil {
		t.Fatalf("handleMove returned error: %v", err)
	}
	if len(result.Content) == 0 {
		t.Fatal("expected result content")
	}
	text := result.Content[0].(mcp.TextContent).Text
	if strings.Contains(text, "move_history") || strings.Contains(text, "current_moves") || strings.Contains(text, "current_moves_count") {
		t.Fatalf("expected history fields omitted from default output, got: %s", text)
	}
}

func TestHandleCreateSession_UsesGraphQLStyleArgsAndPrecedence(t *testing.T) {
	moveDelay := 120
	mockSvc := &mockGameService{
		createSessionFunc: func(ctx context.Context, configName string, opts ...service.CreateSessionOptions) (*service.SessionInfo, error) {
			if configName != "map-id" {
				t.Fatalf("expected map_id precedence, got %q", configName)
			}
			if len(opts) != 1 {
				t.Fatalf("expected single CreateSessionOptions, got %d", len(opts))
			}
			if !opts[0].FogEnabled {
				t.Fatalf("expected fog_enabled to be true")
			}
			if opts[0].FogRadius != 2 {
				t.Fatalf("expected fog_radius=2, got %d", opts[0].FogRadius)
			}
			if opts[0].GridPassword != "pw" {
				t.Fatalf("expected grid_password to be propagated")
			}
			if opts[0].MoveDelayMs == nil || *opts[0].MoveDelayMs != moveDelay {
				t.Fatalf("expected move_delay_ms=%d, got %+v", moveDelay, opts[0].MoveDelayMs)
			}
			return &service.SessionInfo{ID: "s1"}, nil
		},
	}
	server := NewServer(mockSvc, nil)

	_, err := server.handleCreateSession(context.Background(), mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]interface{}{
		"map_id":        "map-id",
		"map_name":      "map-name",
		"fog_enabled":   true,
		"fog_radius":    2.0,
		"grid_password": "pw",
		"move_delay_ms": float64(moveDelay),
	}}})
	if err != nil {
		t.Fatalf("handleCreateSession returned error: %v", err)
	}
}

func TestHandleMoveHistory_AcceptsGraphQLPaginationArgs(t *testing.T) {
	mockSvc := &mockGameService{
		historyFunc: func(ctx context.Context, sessionID string, opts service.HistoryOptions) (*service.HistoryResponse, error) {
			if sessionID != "s-1" {
				t.Fatalf("unexpected sessionID %q", sessionID)
			}
			if opts.Page != 3 || opts.Limit != 25 || opts.Order != "asc" {
				t.Fatalf("unexpected history opts: %+v", opts)
			}
			return &service.HistoryResponse{}, nil
		},
	}
	server := NewServer(mockSvc, nil)

	_, err := server.handleMoveHistory(context.Background(), mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]interface{}{
		"session_id": "s-1",
		"page":       3.0,
		"limit":      25.0,
		"order":      "ASC",
	}}})
	if err != nil {
		t.Fatalf("handleMoveHistory returned error: %v", err)
	}
}

func TestHandleListSessions_ReturnsGraphQLLikeEnvelope(t *testing.T) {
	now := time.Now()
	mockSvc := &mockGameService{
		listSessionsFunc: func(ctx context.Context) ([]*service.SessionInfo, error) {
			return []*service.SessionInfo{
				{ID: "older", CreatedAt: now.Add(-2 * time.Hour), LastActionAt: now.Add(-30 * time.Minute)},
				{ID: "newer", CreatedAt: now.Add(-1 * time.Hour), LastActionAt: now.Add(-10 * time.Minute)},
			}, nil
		},
	}
	server := NewServer(mockSvc, nil)

	result, err := server.handleListSessions(context.Background(), mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]interface{}{
		"sort":  "CREATED",
		"order": "ASC",
		"limit": 1.0,
	}}})
	if err != nil {
		t.Fatalf("handleListSessions returned error: %v", err)
	}
	text := result.Content[0].(mcp.TextContent).Text
	if !strings.Contains(text, "count") || !strings.Contains(text, "total") || !strings.Contains(text, "sessions") {
		t.Fatalf("expected GraphQL-style envelope fields in output, got: %s", text)
	}
	if !strings.Contains(text, "older") || strings.Contains(text, "newer") {
		t.Fatalf("expected created asc sort + limit to keep only oldest session, got: %s", text)
	}
}

// adminCtx carries an X-Admin-Key request into an MCP tool call.
func adminCtx(key string) context.Context {
	req, _ := http.NewRequest(http.MethodPost, "/tesla/mcp", nil)
	if key != "" {
		req.Header.Set("X-Admin-Key", key)
	}
	return context.WithValue(context.Background(), mcpHTTPRequestKey{}, req)
}

func resultText(t *testing.T, r *mcp.CallToolResult) string {
	t.Helper()
	if r == nil {
		t.Fatal("nil tool result")
	}
	var b strings.Builder
	for _, c := range r.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func TestMapMutationsRequireAdmin(t *testing.T) {
	svc := &mockGameService{mapEntry: &service.MapEntry{Name: "mine", Config: &engine.GameConfig{Name: "mine"}}}
	server := NewServer(svc, nil, WithAdminPolicy(appconfig.AdminPolicy{APIKey: "secret"}))

	ops := []struct {
		name string
		call func(context.Context) (*mcp.CallToolResult, error)
	}{
		{"create_map", func(ctx context.Context) (*mcp.CallToolResult, error) {
			return server.handleCreateMap(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]interface{}{"name": "new"}}})
		}},
		{"update_map", func(ctx context.Context) (*mcp.CallToolResult, error) {
			return server.handleUpdateMap(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]interface{}{"name": "mine"}}})
		}},
		{"delete_map", func(ctx context.Context) (*mcp.CallToolResult, error) {
			return server.handleDeleteMap(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]interface{}{"name": "mine"}}})
		}},
	}
	for _, op := range ops {
		t.Run(op.name, func(t *testing.T) {
			for _, ctx := range []context.Context{context.Background(), adminCtx("wrong")} {
				res, err := op.call(ctx)
				if err != nil {
					t.Fatalf("%s: %v", op.name, err)
				}
				if text := resultText(t, res); !strings.Contains(text, "forbidden") {
					t.Fatalf("%s without a valid admin key = %q, want forbidden", op.name, text)
				}
			}
		})
	}
}

func TestAdminMapMutationsUseLifecycleActors(t *testing.T) {
	svc := &mockGameService{mapEntry: &service.MapEntry{
		Name: "mine", Config: &engine.GameConfig{Name: "mine"}, Status: service.MapStatusDraft,
	}}
	server := NewServer(svc, nil, WithAdminPolicy(appconfig.AdminPolicy{APIKey: "secret"}))
	ctx := adminCtx("secret")

	if res, err := server.handleUpdateMap(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]interface{}{
		"name": "mine", "description": "updated",
	}}}); err != nil || strings.Contains(resultText(t, res), "error:") {
		t.Fatalf("admin update: %v %v", resultText(t, res), err)
	}
	if svc.updatedName != "mine" || !svc.updatedActor.Admin {
		t.Fatalf("update used name=%q actor=%+v", svc.updatedName, svc.updatedActor)
	}
	if res, err := server.handleDeleteMap(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]interface{}{
		"name": "mine",
	}}}); err != nil || strings.Contains(resultText(t, res), "error:") {
		t.Fatalf("admin delete: %v %v", resultText(t, res), err)
	}
	if svc.deletedName != "mine" || !svc.deletedActor.Admin {
		t.Fatalf("delete used name=%q actor=%+v", svc.deletedName, svc.deletedActor)
	}
}

func TestGetMapHidesDraftsFromNonAdmins(t *testing.T) {
	cfg := &engine.GameConfig{Name: "draft"}
	svc := &mockGameService{mapEntry: &service.MapEntry{Name: "draft", Config: cfg, OwnerID: "acct-a", Status: service.MapStatusDraft}}
	server := NewServer(svc, nil, WithAdminPolicy(appconfig.AdminPolicy{APIKey: "secret"}))
	req := mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]interface{}{"name": "draft"}}}

	res, err := server.handleGetMap(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if text := resultText(t, res); !strings.Contains(text, "not found") {
		t.Fatalf("non-admin get_map on a draft = %q, want not found", text)
	}

	res, err = server.handleGetMap(adminCtx("secret"), req)
	if err != nil {
		t.Fatal(err)
	}
	if text := resultText(t, res); strings.Contains(text, "not found") {
		t.Fatalf("admin get_map on a draft = %q", text)
	}

	// Published maps stay readable without credentials.
	svc.mapEntry.Status = service.MapStatusPublished
	res, err = server.handleGetMap(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if text := resultText(t, res); strings.Contains(text, "not found") {
		t.Fatalf("published get_map = %q", text)
	}
}

func TestMapMutationsOpenWithDevBypass(t *testing.T) {
	svc := &mockGameService{mapEntry: &service.MapEntry{Name: "mine", Config: &engine.GameConfig{Name: "mine"}}}
	server := NewServer(svc, nil, WithAdminPolicy(appconfig.AdminPolicy{AllowUnauthenticated: true}))
	res, err := server.handleDeleteMap(context.Background(), mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]interface{}{
		"name": "mine",
	}}})
	if err != nil || strings.Contains(resultText(t, res), "error:") {
		t.Fatalf("dev bypass delete: %v %v", resultText(t, res), err)
	}
}
