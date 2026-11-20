// Package api keeps the sign-in HTTP routes. The GraphQL server that used to
// live here moved to the platform's single executor; the standalone test
// harness remains in legacy_server_test.go so resolver tests keep their
// end-to-end transport.
package api

import (
	"net/http"

	"github.com/gorilla/mux"
	"github.com/wricardo/drbrain/games/motor/auth"
)

// NewAuthRoutes returns the sign-in routes as a handler. The gorilla patterns
// stay relative ("/auth/..."); the platform strips the game prefix first.
func NewAuthRoutes(a *auth.Auth) http.Handler {
	r := mux.NewRouter()
	a.Routes(r)
	return r
}
