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

	"github.com/wricardo/drbrain-motor-programming/game/engine"
	"github.com/wricardo/drbrain-motor-programming/game/service"
	"github.com/wricardo/drbrain-motor-programming/transport/websocket"
)

type fakeService struct {
	service.GameService
	maps    []*engine.Map
	builtin map[string]bool
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

func (f *fakeService) IsBuiltinMap(id string) bool { return f.builtin[id] }

func (f *fakeService) SaveMap(cfg engine.MapConfig) (*engine.Map, error) {
	m, err := engine.NewMap(cfg)
	if err != nil {
		return nil, err
	}
	for i, existing := range f.maps {
		if existing.ID == cfg.ID {
			f.maps[i] = m
			return m, nil
		}
	}
	f.maps = append(f.maps, m)
	return m, nil
}

func (f *fakeService) CreateMap(cfg engine.MapConfig) (*engine.Map, error) {
	if _, err := f.GetMap(cfg.ID); err == nil {
		return nil, service.Errorf(service.CodeInvalidArgument, "map %q already exists", cfg.ID)
	}
	return f.SaveMap(cfg)
}

func (f *fakeService) DeleteMap(id string) error {
	for i, m := range f.maps {
		if m.ID == id {
			f.maps = append(f.maps[:i], f.maps[i+1:]...)
			delete(f.builtin, id)
			return nil
		}
	}
	return service.Errorf(service.CodeNotFound, "map %q not found", id)
}

func newTestServer(t *testing.T, opts Options) *httptest.Server {
	t.Helper()
	m, err := engine.NewMap(engine.MapConfig{ID: "m1", Name: "M1", Layout: []string{"...", ">.*", "..."}})
	if err != nil {
		t.Fatal(err)
	}
	opts.LLMSTemplate = "base={{.BaseURL}} ws={{.WSURL}}"
	if opts.UIDirs == nil {
		opts.UIDirs = []string{filepath.Join(t.TempDir(), "missing")}
	}
	s, err := NewServer(&fakeService{maps: []*engine.Map{m}, builtin: map[string]bool{"m1": true}}, websocket.NewHub(), opts)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
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

func TestMapsQueryOverHTTP(t *testing.T) {
	ts := newTestServer(t, Options{Introspection: true})
	status, out := gql(t, ts.URL, `{ maps { id start { x y } startFacing treats { x y } } }`, nil)
	if status != 200 || out["errors"] != nil {
		t.Fatalf("status=%d out=%v", status, out)
	}
	maps := out["data"].(map[string]any)["maps"].([]any)
	if len(maps) != 1 || maps[0].(map[string]any)["startFacing"] != "RIGHT" {
		t.Fatalf("maps = %v", maps)
	}
}

// Map mutations over HTTP: creating a map needs a signed-in player (a guest is
// UNAUTHENTICATED and nothing is stored), a player-created map stays open to
// everyone for edits and deletes, and a shipped map needs the admin key.
func TestMapMutationsOverHTTP(t *testing.T) {
	t.Setenv("ADMIN_API_KEY", "secret")
	t.Setenv("ALLOW_UNAUTHENTICATED_ADMIN", "")
	ts := newPasswordServer(t) // serves the shipped map "wide"
	guest, signedIn := newBrowser(t, ts.URL), newBrowser(t, ts.URL)
	if _, out := signedIn.pwSignIn(true, "map_maker", "tangerine-ladder-42", nil); firstErrCode(out) != "" {
		t.Fatalf("sign in: %v", out)
	}

	const createMutation = `mutation { createMap(map: {id: "mine", name: "Mine", layout: ["...", ">.*", "..."]}) { id builtin } }`
	// A guest cannot create a map, and the store is untouched.
	if out := guest.gql(createMutation, nil); firstErrCode(out) != service.CodeUnauthenticated {
		t.Fatalf("guest create: %v", out)
	}
	if maps := guest.gql(`{ maps { id } }`, nil); strings.Contains(string(mustJSON(maps)), `"mine"`) {
		t.Fatalf("a guest create stored a map: %v", maps)
	}
	// A signed-in player can, without any key.
	out := signedIn.gql(createMutation, nil)
	created, _ := out["data"].(map[string]any)["createMap"].(map[string]any)
	if created == nil || created["builtin"] != false {
		t.Fatalf("create: out=%v", out)
	}
	// Editing a shipped map (wide) needs the admin key.
	const updateShipped = `mutation { updateMap(map: {id: "wide", name: "F", layout: ["...", ">.*", "..."]}) { id } }`
	if out = guest.gql(updateShipped, nil); firstErrCode(out) != service.CodeForbidden {
		t.Fatalf("no key: out=%v", out)
	}
	guest.headers["X-Admin-Key"] = "wrong"
	if out = guest.gql(updateShipped, nil); firstErrCode(out) != service.CodeForbidden {
		t.Fatalf("wrong key: %v", out)
	}
	guest.headers["X-Admin-Key"] = "secret"
	if out = guest.gql(updateShipped, nil); out["errors"] != nil {
		t.Fatalf("right key rejected: %v", out)
	}
	delete(guest.headers, "X-Admin-Key")
	const deleteShipped = `mutation { deleteMap(id: "wide") }`
	if out = guest.gql(deleteShipped, nil); firstErrCode(out) != service.CodeForbidden {
		t.Fatalf("delete no key: %v", out)
	}
	// A player-created map stays fully open: update and delete need no login or key.
	const updateMine = `mutation { updateMap(map: {id: "mine", name: "Mine 2", layout: ["...", ">.*", "..."]}) { id name } }`
	if out = guest.gql(updateMine, nil); out["errors"] != nil {
		t.Fatalf("update own map: %v", out)
	}
	const deleteMine = `mutation { deleteMap(id: "mine") }`
	if out = guest.gql(deleteMine, nil); out["errors"] != nil {
		t.Fatalf("delete own map: %v", out)
	}
	// Deleting a map that does not exist is NOT_FOUND, not silently true.
	if out = guest.gql(deleteMine, nil); firstErrCode(out) != service.CodeNotFound {
		t.Fatalf("delete missing: out=%v", out)
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
		"/maps": true, "/map/abc": true, "/editor": true, "/learn": true, "/sessions": true, "/multi": true,
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
