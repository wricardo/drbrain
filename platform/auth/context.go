package auth

import (
	"context"
	"net/http"

	"github.com/wricardo/drbrain/platform/account"
)

// HTTPRequestKey is the context key under which the GraphQL middleware stores
// the *http.Request, so account mutations can read the origin and headers.
type HTTPRequestKey struct{}

// ViewerKey is the context key under which the GraphQL middleware stores the
// signed-in viewer of a request.
type ViewerKey struct{}

// Viewer is the signed-in player of a request, resolved from the Authorization
// bearer token. A browser never adds that header by itself, so a cross-site
// page cannot act as the player.
type Viewer struct {
	Account *account.Account
	// Token is the raw access token (for logout bookkeeping on the client).
	Token string
}

// ViewerFrom returns the request's viewer, or nil.
func ViewerFrom(ctx context.Context) *Viewer {
	v, _ := ctx.Value(ViewerKey{}).(*Viewer)
	if v == nil || v.Account == nil {
		return nil
	}
	return v
}

// WithViewer stores a viewer in ctx.
func WithViewer(ctx context.Context, v *Viewer) context.Context {
	return context.WithValue(ctx, ViewerKey{}, v)
}

// WithRequest stores the request for mountains that need headers or cookies.
func WithRequest(ctx context.Context, r *http.Request) context.Context {
	return context.WithValue(ctx, HTTPRequestKey{}, r)
}

// RequestFrom returns the request stored by the middleware.
func RequestFrom(ctx context.Context) *http.Request {
	r, _ := ctx.Value(HTTPRequestKey{}).(*http.Request)
	return r
}
