package graph

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	deliverygraph "github.com/wricardo/drbrain/games/delivery/graph"
	mazesgraph "github.com/wricardo/drbrain/games/mazes/graph"
	motorgraph "github.com/wricardo/drbrain/games/motor/graph"
	teslagraph "github.com/wricardo/drbrain/games/tesla/graph"
)

func testHandler(t *testing.T, opts Options) http.Handler {
	t.Helper()
	if opts.Resolver == nil {
		opts.Resolver = New(nil, &deliverygraph.Resolver{}, &mazesgraph.Resolver{}, &motorgraph.Resolver{}, &teslagraph.Resolver{})
	}
	return NewHandler(opts)
}

func TestHandlerNamespacedPost(t *testing.T) {
	h := testHandler(t, Options{Introspection: true})
	body := `{"query":"{ delivery { health { status } } }"}`
	req := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data struct {
			Delivery struct {
				Health struct {
					Status string `json:"status"`
				} `json:"health"`
			} `json:"delivery"`
		} `json:"data"`
		Errors []any `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("errors = %v", resp.Errors)
	}
	if resp.Data.Delivery.Health.Status != "ok" {
		t.Fatalf("status = %q, want ok", resp.Data.Delivery.Health.Status)
	}
}

func TestHandlerIntrospectionToggle(t *testing.T) {
	body := `{"query":"{ __schema { queryType { name } } }"}`
	for _, tc := range []struct {
		name string
		on   bool
	}{
		{"on", true},
		{"off", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := testHandler(t, Options{Introspection: tc.on})
			req := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			got := strings.Contains(rec.Body.String(), "Query")
			if got != tc.on {
				t.Fatalf("introspection exposed = %v, want %v; body = %s", got, tc.on, rec.Body.String())
			}
		})
	}
}

func TestHandlerCORSPreflight(t *testing.T) {
	h := testHandler(t, Options{AllowedOrigins: "https://ok.example"})
	req := httptest.NewRequest(http.MethodOptions, "/graphql", nil)
	req.Header.Set("Origin", "https://ok.example")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://ok.example" {
		t.Fatalf("allow origin = %q", got)
	}
}

func TestHandlerRequestContextKeys(t *testing.T) {
	var gotReq bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotReq = r.Context().Value(mazesgraph.HTTPRequestKey{}) != nil
	})
	withRequestContext(nil, next).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/graphql", nil))
	if !gotReq {
		t.Fatal("mazes HTTPRequestKey was not set")
	}
}

func postJSON(t *testing.T, h http.Handler, body string) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	return out
}

func TestIntrospectionListsNamespaces(t *testing.T) {
	h := testHandler(t, Options{Introspection: true})
	out := postJSON(t, h, `{"query":"{ __schema { queryType { fields { name } } } }"}`)
	data := out["data"].(map[string]any)
	schema := data["__schema"].(map[string]any)
	fields := schema["queryType"].(map[string]any)["fields"].([]any)
	var names []string
	for _, f := range fields {
		names = append(names, f.(map[string]any)["name"].(string))
	}
	want := []string{"account", "delivery", "mazes", "motorProgramming", "tesla"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("Query fields = %v, want %v", names, want)
	}
}

func TestAliasFragmentAndTypename(t *testing.T) {
	h := testHandler(t, Options{Introspection: true})
	out := postJSON(t, h, `{"query":"query Q { d: delivery { __typename ...H } } fragment H on Delivery_Query { health { status } }"}`)
	if errs, ok := out["errors"]; ok {
		t.Fatalf("errors = %v", errs)
	}
	d := out["data"].(map[string]any)["d"].(map[string]any)
	if got := d["__typename"]; got != "Delivery_Query" {
		t.Fatalf("__typename = %v, want Delivery_Query", got)
	}
	if got := d["health"].(map[string]any)["status"]; got != "ok" {
		t.Fatalf("health = %v", got)
	}
}

func TestDisabledNamespaceReportsError(t *testing.T) {
	h := testHandler(t, Options{Resolver: New(nil, &deliverygraph.Resolver{}, nil, nil, nil)})
	out := postJSON(t, h, `{"query":"{ tesla { maps { mapId } } }"}`)
	errs, ok := out["errors"].([]any)
	if !ok || len(errs) == 0 {
		t.Fatalf("expected an error, got %v", out)
	}
	first := errs[0].(map[string]any)
	if !strings.Contains(first["message"].(string), "not enabled") {
		t.Fatalf("message = %q, want not enabled", first["message"])
	}
}
