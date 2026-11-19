package graph

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestGzipCompressesLargeBody proves the snapshot the UI polls is compressed,
// and that it decodes back to the same bytes.
func TestGzipCompressesLargeBody(t *testing.T) {
	// Repetitive JSON, the shape of a real snapshot: many objects with the
	// same keys, which is why the ratio is so high.
	payload := strings.Repeat(`{"id":"o1","state":"EXPIRED","reward":12},`, 500)
	h := gzipHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, payload)
	}))

	req := httptest.NewRequest(http.MethodPost, "/graphql", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	// A wrong length here corrupts the next response on a keep-alive
	// connection, which is far worse than a missing one.
	if rec.Header().Get("Content-Length") != "" {
		t.Errorf("Content-Length was kept on a compressed body: %q", rec.Header().Get("Content-Length"))
	}
	if got := rec.Header().Get("Vary"); !strings.Contains(got, "Accept-Encoding") {
		t.Errorf("Vary = %q, want it to include Accept-Encoding", got)
	}

	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatalf("body is not gzip: %v", err)
	}
	got, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if string(got) != payload {
		t.Errorf("round trip mismatch: %d bytes out, %d in", len(got), len(payload))
	}
	if rec.Body.Len() >= len(payload)/2 {
		t.Errorf("compressed body is %d bytes for a %d byte payload; not compressing",
			rec.Body.Len(), len(payload))
	}
}

// TestGzipSkipsSmallBody pins the threshold: a health probe should not grow.
func TestGzipSkipsSmallBody(t *testing.T) {
	h := gzipHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"data":{"health":{"status":"ok"}}}`)
	}))
	req := httptest.NewRequest(http.MethodPost, "/graphql", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Errorf("small body was encoded as %q", got)
	}
	if body := rec.Body.String(); !strings.Contains(body, "ok") {
		t.Errorf("body = %q", body)
	}
}

// TestGzipRespectsClientRefusal covers both halves of negotiation: a client
// that does not advertise gzip, and one that explicitly refuses it with q=0.
// Sending either an encoded body is unreadable to them.
func TestGzipRespectsClientRefusal(t *testing.T) {
	payload := strings.Repeat("x", 4096)
	h := gzipHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, payload)
	}))

	for _, ae := range []string{"", "identity", "gzip;q=0", "br, gzip;q=0"} {
		req := httptest.NewRequest(http.MethodPost, "/graphql", nil)
		if ae != "" {
			req.Header.Set("Accept-Encoding", ae)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if got := rec.Header().Get("Content-Encoding"); got != "" {
			t.Errorf("Accept-Encoding %q got Content-Encoding %q, want uncompressed", ae, got)
		}
		if rec.Body.String() != payload {
			t.Errorf("Accept-Encoding %q body was mangled", ae)
		}
	}
}

// TestGzipLeavesNoBodyStatusAlone: 204 must stay bodyless. Writing framing
// bytes for an empty payload turns a correct response into a protocol error.
func TestGzipLeavesNoBodyStatusAlone(t *testing.T) {
	h := gzipHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodPost, "/graphql", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("204 carries %d bytes", rec.Body.Len())
	}
	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Errorf("204 was encoded as %q", got)
	}
}

// TestGzipPreservesStatusCodeAndHeaders makes sure the wrapper is invisible to
// the handler it wraps.
func TestGzipPreservesStatusCodeAndHeaders(t *testing.T) {
	h := gzipHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, strings.Repeat(`{"errors":[]},`, 200))
	}))
	req := httptest.NewRequest(http.MethodPost, "/graphql", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
}
