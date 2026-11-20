package httpserve

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newSPA creates a minimal SPA root with an index and one hashed asset.
func newSPA(t *testing.T) http.Handler {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "_app", "immutable"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("SPA-INDEX"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "_app", "immutable", "entry.js"), []byte("ASSET-JS"), 0o644); err != nil {
		t.Fatal(err)
	}
	return SPAHandler(root)
}

// echoHandler reports what the game's own mux saw.
func echoHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"path":     r.URL.Path,
			"rawQuery": r.URL.RawQuery,
			"upgrade":  r.Header.Get("Upgrade"),
		})
	})
}

// upgradeHandler completes a websocket handshake without a library, which is
// all the router has to pass through.
func upgradeHandler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			t.Errorf("game handler saw Upgrade=%q, want websocket", r.Header.Get("Upgrade"))
			http.Error(w, "upgrade required", http.StatusBadRequest)
			return
		}
		if r.URL.Path != "/mcp" {
			t.Errorf("game handler saw path %q, want /mcp", r.URL.Path)
		}
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Error("handler writer does not implement http.Hijacker")
			return
		}
		conn, brw, err := hj.Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		defer conn.Close()
		_, _ = brw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		_ = brw.Flush()
	})
}

func newTestRouter(t *testing.T) (*Router, *httptest.Server) {
	t.Helper()
	r := NewRouter(newSPA(t))
	r.SetLogger(nil)
	if err := r.Mount(Mount{Game: "mazes", Prefix: "/mazes", API: []string{"/graphql", "/graphql/", "/mcp"}, Handler: mazesHandler(t)}); err != nil {
		t.Fatalf("mount mazes: %v", err)
	}
	if err := r.Mount(Mount{Game: "motor", Prefix: "/motor", API: []string{"/graphql"}, Handler: echoHandler()}); err != nil {
		t.Fatalf("mount motor: %v", err)
	}
	srv := httptest.NewServer(r.Handler())
	t.Cleanup(srv.Close)
	return r, srv
}

// mazesHandler dispatches the stripped path the way a game mux would.
func mazesHandler(t *testing.T) http.Handler {
	echo := echoHandler()
	upgrade := upgradeHandler(t)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/mcp" {
			upgrade.ServeHTTP(w, r)
			return
		}
		echo.ServeHTTP(w, r)
	})
}

func TestRouterAcceptance(t *testing.T) {
	_, srv := newTestRouter(t)

	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
		wantBody   string
		wantLoc    string
	}{
		{name: "graphql keeps path and raw query", path: "/mazes/graphql?x=1&y=2", wantStatus: 200,
			wantBody: `"path":"/graphql"`},
		{name: "graphql playground subpath", path: "/mazes/graphql/playground", wantStatus: 200,
			wantBody: `"path":"/graphql/playground"`},
		{name: "spa route under prefix", path: "/mazes/play/abc", wantStatus: 200, wantBody: "SPA-INDEX"},
		{name: "spa route without slash arg", path: "/mazes/maps", wantStatus: 200, wantBody: "SPA-INDEX"},
		{name: "hashed asset", path: "/_app/immutable/entry.js", wantStatus: 200, wantBody: "ASSET-JS"},
		{name: "bare prefix is spa, no redirect", path: "/mazes", wantStatus: 200, wantBody: "SPA-INDEX"},
		{name: "missing asset is 404", path: "/missing.js", wantStatus: 404},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			method := tc.method
			if method == "" {
				method = http.MethodGet
			}
			req, err := http.NewRequest(method, srv.URL+tc.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			}}
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body %q)", resp.StatusCode, tc.wantStatus, body)
			}
			if tc.wantBody != "" && !strings.Contains(string(body), tc.wantBody) {
				t.Errorf("body = %q, want it to contain %q", body, tc.wantBody)
			}
			if tc.name == "graphql keeps path and raw query" {
				var got map[string]string
				if err := json.Unmarshal(body, &got); err != nil {
					t.Fatalf("body is not json: %v", err)
				}
				if got["rawQuery"] != "x=1&y=2" {
					t.Errorf("raw query = %q, want x=1&y=2", got["rawQuery"])
				}
			}
			if loc := resp.Header.Get("Location"); loc != tc.wantLoc {
				t.Errorf("Location = %q, want %q", loc, tc.wantLoc)
			}
		})
	}
}

func TestRouterUpgradesWebsocket(t *testing.T) {
	_, srv := newTestRouter(t)

	u := strings.TrimPrefix(srv.URL, "http://")
	conn, err := net.Dial("tcp", u)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprintf(conn, "GET /mazes/mcp HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n", u)
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(line, "101") {
		t.Fatalf("status line = %q, want 101 Switching Protocols", line)
	}
}

func TestRouterMountValidation(t *testing.T) {
	ok := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})

	tests := []struct {
		name  string
		setup func(*Router)
		mount Mount
		want  string
	}{
		{
			name: "duplicate prefix",
			setup: func(r *Router) {
				r.Mount(Mount{Game: "mazes", Prefix: "/mazes", API: []string{"/graphql"}, Handler: ok})
			},
			mount: Mount{Game: "other", Prefix: "/mazes", API: []string{"/graphql"}, Handler: ok},
			want:  "already mounted",
		},
		{
			name:  "reserved app path",
			setup: func(*Router) {},
			mount: Mount{Game: "bad", Prefix: "/_app", API: []string{"/graphql"}, Handler: ok},
			want:  "reserved",
		},
		{
			name:  "reserved nested full path",
			setup: func(*Router) {},
			mount: Mount{Game: "bad", Prefix: "/mazes", API: []string{"/../_app/x"}, Handler: ok},
			want:  "bad",
		},
		{
			name:  "catch-all api",
			setup: func(*Router) {},
			mount: Mount{Game: "bad", Prefix: "/mazes", API: []string{"/"}, Handler: ok},
			want:  "catch-all",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := NewRouter(newSPA(t))
			tc.setup(r)
			err := r.Mount(tc.mount)
			if err == nil {
				t.Fatalf("Mount(%s) succeeded, want error", tc.mount.Game)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestPanicIsolation(t *testing.T) {
	r := NewRouter(newSPA(t))
	r.SetLogger(nil)
	if err := r.Mount(Mount{Game: "mazes", Prefix: "/mazes", API: []string{"/graphql"}, Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	})}); err != nil {
		t.Fatal(err)
	}
	if err := r.Mount(Mount{Game: "motor", Prefix: "/motor", API: []string{"/graphql"}, Handler: echoHandler()}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(r.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/mazes/graphql")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("panicking game status = %d, want 500", resp.StatusCode)
	}

	resp, err = http.Get(srv.URL + "/motor/graphql")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("second game status = %d, want 200", resp.StatusCode)
	}
}

func TestHealthz(t *testing.T) {
	r := NewRouter(newSPA(t))
	r.SetLogger(nil)
	ok := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	if err := r.Mount(Mount{Game: "mazes", Prefix: "/mazes", API: []string{"/graphql"}, Handler: ok}); err != nil {
		t.Fatal(err)
	}
	if err := r.Mount(Mount{Game: "motor", Prefix: "/motor", API: []string{"/graphql"}, Handler: ok, Health: func() error {
		return fmt.Errorf("database down")
	}}); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	r.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	var got healthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("health body: %v", err)
	}
	if got.Status != "degraded" {
		t.Errorf("status = %q, want degraded", got.Status)
	}
	if got.Games["mazes"] != "ok" {
		t.Errorf("mazes = %q, want ok", got.Games["mazes"])
	}
	if !strings.Contains(got.Games["motor"], "database down") {
		t.Errorf("motor = %q, want the health error", got.Games["motor"])
	}
}

func TestGzipCompressesLargeGameResponse(t *testing.T) {
	r := NewRouter(newSPA(t))
	r.SetLogger(nil)
	payload := strings.Repeat(`{"id":"x","kind":"tile"},`, 200)
	if err := r.Mount(Mount{Game: "mazes", Prefix: "/mazes", API: []string{"/graphql"}, Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, payload)
	})}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(r.Handler())
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/mazes/graphql", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("Content-Encoding"); got != "gzip" {
		t.Errorf("Content-Encoding = %q, want gzip", got)
	}
}

func TestDisabledGamePrefixes404(t *testing.T) {
	r := NewRouter(newSPA(t))
	r.SetLogger(nil)
	if err := r.Mount(Mount{Game: "mazes", Prefix: "/mazes", API: []string{"/graphql"}, Handler: echoHandler()}); err != nil {
		t.Fatalf("mount mazes: %v", err)
	}
	if err := r.Disable("tesla", "/tesla"); err != nil {
		t.Fatalf("disable tesla: %v", err)
	}
	if err := r.Disable("delivery", "/delivery"); err != nil {
		t.Fatalf("disable delivery: %v", err)
	}
	if err := r.Disable("mazes", "/mazes"); err == nil {
		t.Fatal("disabling a mounted prefix must fail")
	}
	srv := httptest.NewServer(r.Handler())
	t.Cleanup(srv.Close)

	tests := []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/tesla", 404},
		{http.MethodGet, "/tesla/", 404},
		{http.MethodGet, "/tesla/maps", 404},
		{http.MethodPost, "/tesla/graphql", 404},
		{http.MethodGet, "/delivery/graphql", 404},
		{http.MethodPost, "/mazes/graphql", 200},
		{http.MethodGet, "/mazes", 200},
	}
	for _, tc := range tests {
		req, err := http.NewRequest(tc.method, srv.URL+tc.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != tc.want {
			t.Errorf("%s %s = %d, want %d", tc.method, tc.path, resp.StatusCode, tc.want)
		}
	}

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var health struct {
		Games map[string]string `json:"games"`
	}
	if err := json.Unmarshal(body, &health); err != nil {
		t.Fatal(err)
	}
	if len(health.Games) != 1 || health.Games["mazes"] != "ok" {
		t.Fatalf("healthz games = %v, want only mazes ok", health.Games)
	}
}

func TestMountAllowsEmptyAPI(t *testing.T) {
	r := NewRouter(newSPA(t))
	r.SetLogger(nil)
	if err := r.Mount(Mount{Game: "delivery", Prefix: "/delivery", Handler: http.NewServeMux()}); err != nil {
		t.Fatalf("Mount(empty API): %v", err)
	}
	rec := httptest.NewRecorder()
	r.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/delivery/play", nil))
	if strings.Contains(rec.Body.String(), "SPA-INDEX") == false {
		t.Fatalf("empty-API prefix did not fall through to the SPA: %q", rec.Body.String())
	}
}

func TestMountPlatformServesSubtree(t *testing.T) {
	r := NewRouter(newSPA(t))
	r.SetLogger(nil)
	platform := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, _ = io.WriteString(w, "platform:"+req.URL.Path)
	})
	if err := r.MountPlatform("/graphql", platform); err != nil {
		t.Fatal(err)
	}
	if err := r.MountPlatform("/graphql", platform); err == nil {
		t.Fatal("duplicate platform route accepted")
	}

	for _, path := range []string{"/graphql", "/graphql/playground"} {
		rec := httptest.NewRecorder()
		r.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if got, want := rec.Body.String(), "platform:"+path; got != want {
			t.Errorf("%s body = %q, want %q", path, got, want)
		}
	}

	rec := httptest.NewRecorder()
	r.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/other", nil))
	if !strings.Contains(rec.Body.String(), "SPA-INDEX") {
		t.Errorf("non-platform path did not reach the SPA: %q", rec.Body.String())
	}
}
