// Package graph is Tesla's GraphQL transport: the resolver tree, engine/session
// conversions and the admin policy for map mutations. The HTTP executor lives
// in the host's unified graph package.
package graph

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"

	"github.com/wricardo/drbrain/games/tesla/appconfig"
	"github.com/wricardo/drbrain/games/tesla/game/service"
	"github.com/wricardo/drbrain/games/tesla/graph/model"
	"github.com/wricardo/drbrain/games/tesla/transport/websocket"
)

// Resolver is the gqlgen dependency injection root.
type Resolver struct {
	Service service.GameService
	Hub     *websocket.Hub
	// Admin is the map-mutation authorization policy (X-Admin-Key).
	Admin appconfig.AdminPolicy
}

// Option configures optional Resolver dependencies.
type Option func(*Resolver)

// WithAdminPolicy sets the map-mutation authorization policy.
func WithAdminPolicy(policy appconfig.AdminPolicy) Option {
	return func(r *Resolver) { r.Admin = policy }
}

// NewResolver builds the resolver root. The optional policies configure what a
// request may change: without WithAdminPolicy, map mutations require the
// X-Admin-Key the environment configured.
func NewResolver(gameService service.GameService, hub *websocket.Hub, opts ...Option) *Resolver {
	r := &Resolver{Service: gameService, Hub: hub}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// HTTPRequestKey is the context key for the *http.Request injected by the
// withHTTPRequest middleware in main.go. Exported so main can use the same key.
type HTTPRequestKey struct{}

// adminAuthorized reports whether the current request carries valid admin
// credentials (X-Admin-Key matches ADMIN_API_KEY, or the local-dev bypass).
// Unlike checkAdminKey it never errors, so callers can use it as an override.
func (r *Resolver) adminAuthorized(ctx context.Context) bool {
	req, _ := ctx.Value(HTTPRequestKey{}).(*http.Request)
	return r.Admin.Authorized(req)
}

// annotateMapOwnership fills GameMap.builtin/status/ownedByMe from the map's
// catalogue entry. Session payloads embed a map too, so every conversion goes
// through here. `obj` must come straight from toGameMap (never from createMap).
func (r *Resolver) annotateMapOwnership(ctx context.Context, obj *model.GameMap, mapName string) *model.GameMap {
	if obj == nil {
		return nil
	}
	entry, err := r.Service.GetMapEntry(ctx, mapName)
	if err != nil {
		// A map that vanished between load and annotation keeps the legacy
		// builtin answer and must still emit a valid non-null enum.
		obj.Builtin = r.mapBuiltin(ctx, mapName)
		obj.Status = model.MapStatusPublished
		return obj
	}
	fillMapMeta(obj, entry, viewerID(ctx))
	return obj
}

// annotateSessionMap fills the embedded GameMap.builtin for session payloads.
func (r *Resolver) annotateSessionMap(ctx context.Context, session *model.Session, info *service.SessionInfo) *model.Session {
	if session == nil || info == nil {
		return session
	}
	r.annotateMapOwnership(ctx, session.GameMap, info.MapName)
	return session
}

// mapBuiltin reports whether name is a shipped map, tolerating lookups on
// names that no longer resolve.
func (r *Resolver) mapBuiltin(ctx context.Context, name string) bool {
	builtin, exists, err := r.Service.MapAuthorization(ctx, name)
	return err == nil && exists && builtin
}

type uiAuthConfig struct {
	UIMapPassword string `json:"uiMapPassword"`
}

var (
	//go:embed ui-auth.json
	embeddedUIAuthJSON []byte
	uiAuthOnce         sync.Once
	uiMapPassword      string
)

func loadUIMapPassword() string {
	uiAuthOnce.Do(func() {
		var cfg uiAuthConfig
		if err := json.Unmarshal(embeddedUIAuthJSON, &cfg); err != nil {
			uiMapPassword = ""
			return
		}
		uiMapPassword = strings.TrimSpace(cfg.UIMapPassword)
	})
	return uiMapPassword
}

// checkUIMapPassword gates privileged UI map reads (map query) behind the shared ui-auth.json password.
// If the config password is empty/unset, access is allowed for backward compatibility.
func checkUIMapPassword(password *string) error {
	required := loadUIMapPassword()
	if required == "" {
		return nil
	}
	if password == nil || *password != required {
		return errors.New("forbidden: invalid or missing map password")
	}
	return nil
}
