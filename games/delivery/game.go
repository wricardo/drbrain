//go:generate go run github.com/wricardo/drbrain/cmd/split-schema
//go:generate go run github.com/99designs/gqlgen generate

package delivery

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/kelseyhightower/envconfig"
	cityconfig "github.com/wricardo/drbrain/games/delivery/game/config"
	"github.com/wricardo/drbrain/games/delivery/game/session"
	"github.com/wricardo/drbrain/games/delivery/graph"
	"github.com/wricardo/drbrain/platform"
	"github.com/wricardo/drbrain/platform/mapstore"
)

const prefix = "/delivery"

// llmsTemplate is the agent guide, rendered by the platform at /delivery/llms.txt.
//
//go:embed llms.txt.tmpl
var llmsTemplate string

// Env is this game's environment section: DELIVERY_*.
type Env struct {
	// City is the legacy hand-authored default city file.
	City string `envconfig:"CITY" default:"maps/city.json"`
	// CitiesDir is the builtin city directory. When set, every <id>.json in it
	// is a shipped city and City must name the default one (defaulting to
	// <CitiesDir>/city.json). When empty, the directory containing City is the
	// builtin directory, which is how pre-catalogue deployments keep working.
	CitiesDir string `envconfig:"CITIES_DIR"`
	// MaxUserCities caps community cities stored in PostgreSQL.
	MaxUserCities int `envconfig:"MAX_USER_CITIES" default:"200"`
	// AdminAPIKey gates shipped/legacy city writes: a request must send it as
	// X-Admin-Key. Empty disables admin operations unless the opt-out below is
	// set.
	AdminAPIKey string `envconfig:"ADMIN_API_KEY"`
	// AllowUnauthenticatedAdmin permits admin writes while no key is configured
	// (local development only; ignored once AdminAPIKey is set).
	AllowUnauthenticatedAdmin bool `envconfig:"ALLOW_UNAUTHENTICATED_ADMIN"`
}

func loadEnv() (Env, error) {
	var e Env
	if err := envconfig.Process("DELIVERY", &e); err != nil {
		return Env{}, err
	}
	return e, nil
}

// citySetup is the resolved builtin directory and the default city id.
type citySetup struct {
	BuiltinDir  string
	DefaultID   string
	DefaultFile string
}

// resolveCities implements the compatibility rule: CITIES_DIR selects a
// builtin directory, while the legacy CITY file is always the default city and
// must live in it. Without CITIES_DIR the file's own directory becomes the
// builtin directory, so an existing single-file deployment needs no new
// variables. The default id is the default file's base, which is "city" for the
// shipped map and preserves the API's implicit default.
func resolveCities(env Env) (citySetup, error) {
	if dir := strings.TrimSpace(env.CitiesDir); dir != "" {
		absDir, err := filepath.Abs(dir)
		if err != nil {
			return citySetup{}, fmt.Errorf("DELIVERY_CITIES_DIR %q: %w", dir, err)
		}
		info, err := os.Stat(absDir)
		if err != nil {
			return citySetup{}, fmt.Errorf("DELIVERY_CITIES_DIR %q: %w", dir, err)
		}
		if !info.IsDir() {
			return citySetup{}, fmt.Errorf("DELIVERY_CITIES_DIR %q is not a directory", dir)
		}

		var file string
		if explicit := strings.TrimSpace(os.Getenv("DELIVERY_CITY")); explicit != "" {
			file, err = filepath.Abs(strings.TrimSpace(env.City))
			if err != nil {
				return citySetup{}, fmt.Errorf("DELIVERY_CITY %q: %w", env.City, err)
			}
			if filepath.Dir(file) != absDir {
				return citySetup{}, fmt.Errorf(
					"DELIVERY_CITY %s must be the exact default file inside DELIVERY_CITIES_DIR %s",
					file, absDir)
			}
		} else {
			file = filepath.Join(absDir, "city.json")
		}
		id := cityIDFromFile(file)
		if id == "" {
			return citySetup{}, fmt.Errorf("DELIVERY_CITY %s must be a <id>.json file", file)
		}
		return citySetup{BuiltinDir: absDir, DefaultID: id, DefaultFile: file}, nil
	}

	if strings.TrimSpace(env.City) == "" {
		return citySetup{}, fmt.Errorf("DELIVERY_CITY is empty")
	}
	file, err := filepath.Abs(env.City)
	if err != nil {
		return citySetup{}, fmt.Errorf("DELIVERY_CITY %q: %w", env.City, err)
	}
	id := cityIDFromFile(file)
	if id == "" {
		return citySetup{}, fmt.Errorf("DELIVERY_CITY %s must be a <id>.json file", file)
	}
	return citySetup{BuiltinDir: filepath.Dir(file), DefaultID: id, DefaultFile: file}, nil
}

func cityIDFromFile(path string) string {
	base := filepath.Base(path)
	if !strings.HasSuffix(base, ".json") {
		return ""
	}
	return strings.TrimSuffix(base, ".json")
}

// Game is DrBrain Delivery on the platform. The tick loop stays inside each
// session; the host only owns the listener and lifecycle.
type Game struct {
	env       Env
	publicURL string
	mgr       *session.Manager
	resolver  *graph.Resolver
}

// New returns the game for the compiled-in registry.
func New() *Game { return &Game{} }

// Name returns the registry name, "delivery". It is also the GraphQL namespace
// and the DELIVERY_ environment prefix.
func (g *Game) Name() string { return "delivery" }

// Prefix is the SPA mount path, "/delivery". GraphQL does not live here: the
// host serves every operation at "/graphql" under the delivery namespace.
func (g *Game) Prefix() string { return prefix }

// API returns nil. Delivery has no game-specific HTTP routes: the host serves
// the SPA at Prefix and the unified GraphQL handler at "/graphql". Mount
// registers nothing on the mux.
func (g *Game) API() []string { return nil }

// GraphQLResolver implements platform.GraphQLResolver.
func (g *Game) GraphQLResolver() any { return g.resolver }

// LLMSTemplate implements platform.LLMSProvider.
func (g *Game) LLMSTemplate() string { return llmsTemplate }

// LLMSPublicURL implements platform.LLMSProvider.
func (g *Game) LLMSPublicURL() string { return g.publicURL }

// Mount loads DELIVERY_* configuration, opens the city catalogue, requires
// PostgreSQL, and installs the GraphQL resolver. It registers no HTTP routes:
// the host owns "/graphql" and the SPA fallback, so the mux argument is
// reserved for games that need one.
//
// Every builtin city is validated before the listener binds, so a bad shipped
// map fails startup with the file named; corrupt community rows are logged and
// skipped. Sessions are loaded on demand, so startup does not read saved games.
func (g *Game) Mount(mux *http.ServeMux, deps platform.Deps) error {
	env, err := loadEnv()
	if err != nil {
		return fmt.Errorf("DELIVERY_ environment: %w", err)
	}
	g.env = env
	g.publicURL = deps.Config.PublicURL

	setup, err := resolveCities(env)
	if err != nil {
		return fmt.Errorf("DELIVERY_ environment: %w", err)
	}
	catalog, err := cityconfig.Open(cityconfig.Options{
		BuiltinDir:    setup.BuiltinDir,
		DefaultCityID: setup.DefaultID,
		Pool:          deps.DB,
		MaxUserCities: env.MaxUserCities,
	})
	if err != nil {
		return err
	}
	if deps.DB == nil {
		return fmt.Errorf("delivery: PostgreSQL is required (DRBRAIN_DATABASE_URL is unset)")
	}
	mgr, err := session.NewManager(catalog, deps.DB)
	if err != nil {
		return fmt.Errorf("session manager: %w", err)
	}
	if deps.Accounts != nil {
		deps.Accounts.AddOnDelete(func(ownerID string) error {
			// Sessions on this player's drafts must go with the drafts, before
			// the rows disappear and their visibility can no longer be judged.
			var errs []error
			entries, err := catalog.ListFor(ownerID)
			if err != nil {
				errs = append(errs, fmt.Errorf("list cities for %s: %w", ownerID, err))
			} else {
				for _, e := range entries {
					if e.Status == mapstore.StatusDraft && e.OwnerID == ownerID {
						if _, err := mgr.DeleteByCity(e.ID); err != nil {
							errs = append(errs, err)
						}
					}
				}
			}
			errs = append(errs, catalog.OnAccountDelete(ownerID))
			return errors.Join(errs...)
		})
	}
	if deps.Metrics != nil {
		if err := deps.Metrics.Register(cityMoneyCollector{live: mgr.LiveSummaries}); err != nil {
			return fmt.Errorf("delivery metrics: %w", err)
		}
	}
	g.mgr = mgr
	g.resolver = &graph.Resolver{
		Mgr:                       mgr,
		Catalog:                   catalog,
		Accounts:                  deps.Accounts,
		AdminKey:                  env.AdminAPIKey,
		AllowUnauthenticatedAdmin: env.AllowUnauthenticatedAdmin,
	}

	deps.Logger.Info("delivery mounted",
		"cities", setup.BuiltinDir, "default_city", setup.DefaultID,
		"default_city_file", setup.DefaultFile, "max_user_cities", env.MaxUserCities,
		"admin_key", env.AdminAPIKey != "", "allow_unauthenticated_admin", env.AllowUnauthenticatedAdmin)
	return nil
}

// Shutdown saves every session (Manager.Close → SaveAll) so a restart reloads
// paused with the same ledger and progress. Its error is the host's non-zero
// exit.
func (g *Game) Shutdown(context.Context) error {
	if g.mgr == nil {
		return nil
	}
	return g.mgr.Close()
}
