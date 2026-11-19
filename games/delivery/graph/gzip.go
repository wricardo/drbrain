package graph

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// gzipPool reuses writers, because allocating one per poll at 13 requests per
// second per tab is needless garbage in the hottest path the server has.
var gzipPool = sync.Pool{
	New: func() any {
		w, _ := gzip.NewWriterLevel(nil, gzip.BestSpeed)
		return w
	},
}

// gzipMinBytes is the payload size below which compressing costs more than it
// saves: a health probe already ships in one packet.
const gzipMinBytes = 512

// gzipHandler compresses responses for clients that accept it.
//
// The whole response is buffered before the encoding is chosen, which is safe
// here because a normal response on this route is one complete JSON document
// built from a single snapshot read (the largest observed is a 1.3 MB snapshot
// of a 42k-tick city). That buys a much simpler implementation than a streaming
// wrapper, which needs to handle flush, hijack and mid-body header commits.
//
// Streaming requests are passed STRAIGHT THROUGH, uncompressed, and this is not
// an optimisation — it is required for correctness. bufferedResponse implements
// neither http.Flusher nor http.Hijacker, so an SSE or WebSocket subscription
// sent through it can never flush: SSE fails outright with "streaming
// unsupported", and the response would in any case only be written when the
// handler returned, which for a subscription is never. A subscription is also
// unbounded, so buffering it would grow without limit.
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

// isStreaming reports whether a request wants a response that never completes.
//
// Two signals: SSE asks for text/event-stream in Accept, and a WebSocket upgrade
// asks for a protocol switch. Both must bypass buffering.
func isStreaming(r *http.Request) bool {
	if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
		return true
	}
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}

// acceptsGzip reports whether the client wants gzip.
//
// The q=0 check matters: "gzip;q=0" explicitly refuses the encoding, and a
// prefix match alone would send it a body it cannot read.
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
	// skip is set for responses that must not gain an encoding: no-body status
	// codes, where an encoder would only add framing bytes for nothing.
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

// flush writes headers, body and encoding. It deletes Content-Length in every
// case: the buffered length is not the length the client will receive once the
// body is compressed, and a stale length on a keep-alive connection makes the
// client read the next response's bytes as this one's body.
func (b *bufferedResponse) flush() {
	if b.skip {
		b.ResponseWriter.WriteHeader(b.status)
		return
	}
	b.Header().Del("Content-Length")
	b.Header().Add("Vary", "Accept-Encoding")

	if b.body.Len() < gzipMinBytes {
		// Too small to be worth it: send it plainly with an accurate length.
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
	// Reset to nil so a pooled writer does not hold the ResponseWriter alive.
	gz.Reset(nil)
}
