package httpserve

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// gzipPool reuses writers; a snapshot polled several times a second would
// otherwise allocate one per response.
var gzipPool = sync.Pool{
	New: func() any {
		w, _ := gzip.NewWriterLevel(nil, gzip.BestSpeed)
		return w
	},
}

// gzipMinBytes is below the size where compressing costs more than it saves.
const gzipMinBytes = 512

// gzipHandler compresses responses for clients that accept it.
//
// The whole response is buffered before the encoding is chosen. Streaming
// requests (WebSocket upgrades and SSE) pass straight through, uncompressed:
// bufferedResponse implements neither http.Flusher nor http.Hijacker, so a
// subscription could never flush through it. This mirrors games/*/graph/gzip.go.
func gzipHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isStreaming(r) || !acceptsGzip(r) {
			next.ServeHTTP(w, r)
			return
		}
		bw := &bufferedResponse{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(bw, r)
		bw.flush()
	})
}

// isStreaming reports whether the request wants a response that never
// completes: SSE or a WebSocket upgrade.
func isStreaming(r *http.Request) bool {
	if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
		return true
	}
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}

// acceptsGzip reports whether the client wants gzip. "gzip;q=0" explicitly
// refuses the encoding, so a prefix match alone would be wrong.
func acceptsGzip(r *http.Request) bool {
	for _, enc := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		enc = strings.TrimSpace(enc)
		if !strings.HasPrefix(enc, "gzip") {
			continue
		}
		return !strings.Contains(enc, "q=0")
	}
	return false
}

// bufferedResponse collects a handler's output so it can be encoded once.
type bufferedResponse struct {
	http.ResponseWriter
	status    int
	wroteHead bool
	body      bytes.Buffer
	// skip is set for no-body status codes, where an encoder would only add
	// framing bytes.
	skip bool
}

func (b *bufferedResponse) WriteHeader(status int) {
	if b.wroteHead {
		return
	}
	b.status = status
	b.wroteHead = true
	b.skip = status == http.StatusNoContent || status == http.StatusNotModified
}

func (b *bufferedResponse) Write(p []byte) (int, error) {
	if !b.wroteHead {
		b.WriteHeader(http.StatusOK)
	}
	if b.skip {
		return len(p), nil
	}
	return b.body.Write(p)
}

// flush writes headers, body and encoding. Content-Length is deleted in every
// case: a stale length on a keep-alive connection makes the client read the
// next response's bytes as this one's body.
func (b *bufferedResponse) flush() {
	if b.skip {
		b.ResponseWriter.WriteHeader(b.status)
		return
	}
	b.Header().Del("Content-Length")
	b.Header().Add("Vary", "Accept-Encoding")

	if b.body.Len() < gzipMinBytes {
		b.Header().Set("Content-Length", strconv.Itoa(b.body.Len()))
		b.ResponseWriter.WriteHeader(b.status)
		_, _ = b.ResponseWriter.Write(b.body.Bytes())
		return
	}

	gz := gzipPool.Get().(*gzip.Writer)
	defer gzipPool.Put(gz)
	gz.Reset(b.ResponseWriter)
	b.Header().Set("Content-Encoding", "gzip")
	b.ResponseWriter.WriteHeader(b.status)
	_, _ = gz.Write(b.body.Bytes())
	_ = gz.Close()
	// Do not let the pooled writer hold the ResponseWriter alive.
	gz.Reset(nil)
}
