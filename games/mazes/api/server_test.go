package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wricardo/drbrain/games/mazes/game/engine"
	"github.com/wricardo/drbrain/games/mazes/game/service"
	"github.com/wricardo/drbrain/games/mazes/transport/websocket"
	"github.com/wricardo/drbrain/platform/account"
	"github.com/wricardo/drbrain/platform/auth"
)

type fakeService struct {
	service.GameService
	maps []*engine.Map
}

func (f *fakeService) ListMaps() []*engine.Map { return f.maps }

func (f *fakeService) GetMap(id string) (*engine.Map, error) {
	for _, m := range f.maps {
		if m.ID == id {
			return m, nil
		}
	}
	return nil, service.Errorf(service.CodeNotFound, "map %q not found", id)
}

func (f *fakeService) SaveMap(cfg engine.MapConfig) (*engine.Map, error) { return engine.NewMap(cfg) }

func (f *fakeService) CreateMap(cfg engine.MapConfig) (*engine.Map, error) {
	if _, err := f.GetMap(cfg.ID); err == nil {
		return nil, service.Errorf(service.CodeInvalidArgument, "map %q already exists", cfg.ID)
	}
	return engine.NewMap(cfg)
}

func (f *fakeService) ListMapEntries(viewerID string) ([]*service.MapEntry, error) {
	out := make([]*service.MapEntry, 0, len(f.maps))
	for _, m := range f.maps {
		out = append(out, &service.MapEntry{ID: m.ID, Config: m.Config(), Status: service.MapStatusPublished})
	}
	return out, nil
}

func (f *fakeService) GetMapEntry(id string) (*service.MapEntry, error) {
	m, err := f.GetMap(id)
	if err != nil {
		return nil, err
	}
	return &service.MapEntry{ID: m.ID, Config: m.Config(), Status: service.MapStatusPublished}, nil
}

func (f *fakeService) CreateMapDraft(cfg engine.MapConfig, ownerID string) (*service.MapEntry, error) {
	m, err := engine.NewMap(cfg)
	if err != nil {
		return nil, service.Errorf(service.CodeInvalidArgument, "invalid map: %v", err)
	}
	f.maps = append(f.maps, m)
	return &service.MapEntry{ID: m.ID, Config: m.Config(), OwnerID: ownerID, Status: service.MapStatusDraft}, nil
}

func (f *fakeService) UpdateMapOwned(cfg engine.MapConfig, actor service.MapActor) (*service.MapEntry, error) {
	m, err := engine.NewMap(cfg)
	if err != nil {
		return nil, service.Errorf(service.CodeInvalidArgument, "invalid map: %v", err)
	}
	for i, old := range f.maps {
		if old.ID == m.ID {
			f.maps[i] = m
			return &service.MapEntry{ID: m.ID, Config: m.Config(), OwnerID: actor.AccountID, Status: service.MapStatusDraft}, nil
		}
	}
	return nil, service.Errorf(service.CodeNotFound, "map %q not found", m.ID)
}

func (f *fakeService) PublishMap(id string, actor service.MapActor) (*service.MapEntry, error) {
	m, err := f.GetMap(id)
	if err != nil {
		return nil, err
	}
	return &service.MapEntry{ID: m.ID, Config: m.Config(), OwnerID: actor.AccountID, Status: service.MapStatusPublished}, nil
}

func (f *fakeService) DeleteMapOwned(id string, actor service.MapActor) error {
	for i, m := range f.maps {
		if m.ID == id {
			f.maps = append(f.maps[:i], f.maps[i+1:]...)
			return nil
		}
	}
	return service.Errorf(service.CodeNotFound, "map %q not found", id)
}

func newTestServer(t *testing.T, opts Options) *httptest.Server {
	t.Helper()
	m, err := engine.NewMap(engine.MapConfig{ID: "m1", Name: "M1", Levels: [][]string{{"...", "SoF", "..."}}})
	if err != nil {
		t.Fatal(err)
	}
	opts.LLMSTemplate = "base={{.BaseURL}} ws={{.WSURL}}"
	if opts.UIDirs == nil {
		opts.UIDirs = []string{filepath.Join(t.TempDir(), "missing")}
	}
	s, err := NewServer(&fakeService{maps: []*engine.Map{m}}, websocket.NewHub(), opts)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := auth.WithRequest(r.Context(), r)
		if acct := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "); acct != "" && acct != r.Header.Get("Authorization") {
			ctx = auth.WithViewer(ctx, &auth.Viewer{Account: &account.Account{ID: acct}})
		}
		s.ServeHTTP(w, r.WithContext(ctx))
	}))
	t.Cleanup(ts.Close)
	return ts
}

func gql(t *testing.T, url, query string, headers map[string]string) (int, map[string]any) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"query": query})
	req, _ := http.NewRequest(http.MethodPost, url+"/graphql", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	raw, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

func firstErrCode(out map[string]any) string {
	errs, _ := out["errors"].([]any)
	if len(errs) == 0 {
		return ""
	}
	ext, _ := errs[0].(map[string]any)["extensions"].(map[string]any)
	code, _ := ext["code"].(string)
	return code
}

const createMapMutation = `mutation { createMap(map: {id: "fresh", name: "F", levels: [["...", "SoF", "..."]]}) { id levels } }`

func TestMapsQueryOverHTTP(t *testing.T) {
	ts := newTestServer(t, Options{Introspection: true})
	status, out := gql(t, ts.URL, `{ maps { id levelCount preview maxMoves parMoves } }`, nil)
	if status != 200 || out["errors"] != nil {
		t.Fatalf("status=%d out=%v", status, out)
	}
	maps := out["data"].(map[string]any)["maps"].([]any)
	if len(maps) != 1 {
		t.Fatalf("maps = %v", maps)
	}
	m := maps[0].(map[string]any)
	if m["levelCount"] != float64(1) || m["maxMoves"] != float64(engine.DefaultMaxMoves) || m["parMoves"] != float64(2) || len(m["preview"].([]any)) != 3 {
		t.Fatalf("map = %v", m)
	}
}

func TestCreateMapRequiresSignInOverHTTP(t *testing.T) {
	t.Setenv("ADMIN_API_KEY", "")
	t.Setenv("ALLOW_UNAUTHENTICATED_ADMIN", "")
	ts := newTestServer(t, Options{})

	if _, out := gql(t, ts.URL, createMapMutation, nil); firstErrCode(out) != "UNAUTHENTICATED" {
		t.Fatalf("guest createMap: %v", out)
	}
	if _, out := gql(t, ts.URL, createMapMutation, map[string]string{"Authorization": "Bearer acct-1"}); out["errors"] != nil {
		t.Fatalf("signed-in createMap rejected: %v", out)
	}
}

func TestIntrospectionGate(t *testing.T) {
	const q = `{ __schema { queryType { name } } }`
	on := newTestServer(t, Options{Introspection: true})
	if _, out := gql(t, on.URL, q, nil); out["errors"] != nil {
		t.Fatalf("introspection should work: %v", out)
	}
	off := newTestServer(t, Options{Introspection: false})
	if _, out := gql(t, off.URL, q, nil); out["errors"] == nil {
		t.Fatalf("introspection should be disabled: %v", out)
	}
}

func TestPlaygroundGate(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		ts := newTestServer(t, Options{Playground: enabled})
		resp, err := http.Get(ts.URL + "/playground")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if enabled && resp.StatusCode != 200 {
			t.Fatalf("playground enabled: status %d", resp.StatusCode)
		}
		if !enabled && resp.StatusCode != 404 {
			t.Fatalf("playground disabled: status %d", resp.StatusCode)
		}
	}
}

func TestRequestBodyCap(t *testing.T) {
	ts := newTestServer(t, Options{})
	big := `{"query":"{ maps { id } }","pad":"` + strings.Repeat("a", MaxRequestBodyBytes) + `"}`
	resp, err := http.Post(ts.URL+"/graphql", "application/json", strings.NewReader(big))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), "request body too large") {
		t.Fatalf("oversized body accepted: %d %.200s", resp.StatusCode, b)
	}
}

func TestComplexityLimit(t *testing.T) {
	ts := newTestServer(t, Options{})
	// Deeply repeated selections exceed the fixed complexity limit.
	sel := strings.Repeat("a: maps { id } ", ComplexityLimit+1)
	_, out := gql(t, ts.URL, "{ "+sel+"}", nil)
	if out["errors"] == nil {
		t.Fatal("complexity limit not enforced")
	}
}

func TestLLMSTxt(t *testing.T) {
	ts := newTestServer(t, Options{})
	resp, err := http.Get(ts.URL + "/llms.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	want := "base=" + ts.URL + " ws=ws" + strings.TrimPrefix(ts.URL, "http")
	if string(b) != want {
		t.Fatalf("llms = %q, want %q", b, want)
	}
}

func TestIsSPARoute(t *testing.T) {
	cases := map[string]bool{
		"/": true, "": true, "/play": true, "/play/abc": true, "/watch/abc": true,
		"/maps": true, "/editor": true, "/learn": true, "/sessions": true, "/multi": true,
		"/nope": false, "/graphql/x": false, "/playground": false, "/_app/x.js": false,
	}
	for path, want := range cases {
		if got := isSPARoute(path); got != want {
			t.Errorf("isSPARoute(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestSPAServing(t *testing.T) {
	dir := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.WriteFile(filepath.Join(dir, "index.html"), []byte("INDEX"), 0o644))
	must(os.MkdirAll(filepath.Join(dir, "_app", "immutable"), 0o755))
	must(os.WriteFile(filepath.Join(dir, "_app", "immutable", "a.js"), []byte("JS"), 0o644))
	ts := newTestServer(t, Options{UIDirs: []string{dir}})

	get := func(p string) (*http.Response, string) {
		resp, err := http.Get(ts.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp, string(b)
	}
	if r, b := get("/play/abc"); r.StatusCode != 200 || b != "INDEX" {
		t.Fatalf("spa route: %d %q", r.StatusCode, b)
	}
	if r, b := get("/unknown"); r.StatusCode != 404 || b != "INDEX" {
		t.Fatalf("unknown route: %d %q", r.StatusCode, b)
	}
	r, b := get("/_app/immutable/a.js")
	if r.StatusCode != 200 || b != "JS" || !strings.Contains(r.Header.Get("Cache-Control"), "immutable") {
		t.Fatalf("asset: %d %q cc=%q", r.StatusCode, b, r.Header.Get("Cache-Control"))
	}
}

func TestWebsocketOriginCheck(t *testing.T) {
	check := makeCheckOrigin([]string{"http://ok.example"})
	mk := func(origin string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/graphql", nil)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		return r
	}
	if !check(mk("http://ok.example")) || !check(mk("")) {
		t.Fatal("allowed origin / no origin must pass")
	}
	if check(mk("http://evil.example")) {
		t.Fatal("foreign origin must be rejected")
	}
	if !makeCheckOrigin(nil)(mk("http://anything")) {
		t.Fatal("empty allow list must allow all")
	}
	if up := newUpgrader("*"); !up.CheckOrigin(mk("http://anything")) {
		t.Fatal("* must allow all")
	}
	if up := newUpgrader("http://a, http://b"); !up.CheckOrigin(mk("http://b")) || up.CheckOrigin(mk("http://c")) {
		t.Fatal("list parsing wrong")
	}
}
