package auth

import (
	"context"
	"net/http"
	"time"

	"github.com/wricardo/drbrain/platform/account"
)

// HTTPRequestKey is the context key under which the GraphQL middleware stores
// the *http.Request, so account mutations can read the cookie and origin.
type HTTPRequestKey struct{}

// ViewerKey is the context key under which the GraphQL middleware stores the
// signed-in viewer of a request.
type ViewerKey struct{}

// SignInKey is the context key under which the GraphQL middleware stores the
// hooks that need the response (setting the login cookie).
type SignInKey struct{}

// Viewer is the signed-in player of a request. It is only set when the login
// cookie is valid and the request passed the same-origin check, so a cross-site
// page can never act as the player.
type Viewer struct {
	Account *account.Account
	// Token is the login cookie's value (for logout).
	Token string
	// ClearSession removes the login cookie from the response.
	ClearSession func()
}

// SignIn is what password mutations need from the transport. OverHTTP is false
// for a websocket connection, which has no response to set a cookie on.
type SignIn struct {
	OverHTTP   bool
	SetSession func(token string, expires time.Time)
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

// SignInFrom returns the request's sign-in hooks, or nil.
func SignInFrom(ctx context.Context) *SignIn {
	s, _ := ctx.Value(SignInKey{}).(*SignIn)
	return s
}

// WithSignIn stores the sign-in hooks in ctx.
func WithSignIn(ctx context.Context, s *SignIn) context.Context {
	return context.WithValue(ctx, SignInKey{}, s)
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
