package appconfig

import (
	"net/http"
	"testing"
)

// The flag semantics are a public contract: operators rely on "yes"/"1" being
// truthy and any other value disabling the feature, with empty keeping defaults.

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ConfigDir != "maps" {
		t.Errorf("unexpected directory defaults: %+v", cfg)
	}
	if cfg.MaxUserMaps != DefaultMaxUserMaps {
		t.Errorf("MaxUserMaps = %d, want %d", cfg.MaxUserMaps, DefaultMaxUserMaps)
	}
	if cfg.CustomMapsDir != "" {
		t.Errorf("CustomMapsDir must be empty by default: %+v", cfg)
	}
	if !bool(cfg.GraphQLIntrospection) || !bool(cfg.GraphQLPlayground) || !bool(cfg.MCPEnabled) {
		t.Errorf("feature gates must default to enabled: %+v", cfg)
	}
	if cfg.AdminAPIKey != "" || bool(cfg.AllowUnauthenticatedAdmin) {
		t.Errorf("admin must default to closed: %+v", cfg)
	}
	if cfg.AllowedOrigins != nil {
		t.Errorf("origins must default to nil (allow all), got %v", cfg.AllowedOrigins)
	}
}

func TestMaxUserMapsOverride(t *testing.T) {
	t.Setenv("TESLA_MAX_USER_MAPS", "7")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.MaxUserMaps != 7 {
		t.Errorf("MaxUserMaps = %d, want 7", cfg.MaxUserMaps)
	}
}

// TESLA_CUSTOM_MAPS_DIR is deprecated: it is still parsed so existing
// deployments do not fail, but it is not storage.
func TestDeprecatedCustomMapsDirStillParses(t *testing.T) {
	t.Setenv("TESLA_CUSTOM_MAPS_DIR", "/var/lib/drbrain/tesla/maps-custom")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.CustomMapsDir != "/var/lib/drbrain/tesla/maps-custom" {
		t.Errorf("CustomMapsDir = %q", cfg.CustomMapsDir)
	}
}

func TestFlagsAcceptForgivingTrue(t *testing.T) {
	for _, value := range []string{"true", "1", "yes"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("TESLA_MCP_ENABLED", value)
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if !bool(cfg.MCPEnabled) {
				t.Errorf("MCP_ENABLED=%q should be true", value)
			}
		})
	}
}

func TestFlagsRejectAnythingElse(t *testing.T) {
	for _, value := range []string{"false", "0", "no", "on", "TRUE"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("TESLA_MCP_ENABLED", value)
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if bool(cfg.MCPEnabled) {
				t.Errorf("MCP_ENABLED=%q should be false", value)
			}
		})
	}
}

func TestEmptyValueKeepsDefault(t *testing.T) {
	t.Setenv("TESLA_GRAPHQL_PLAYGROUND", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !bool(cfg.GraphQLPlayground) {
		t.Error("an empty GRAPHQL_PLAYGROUND must keep the enabled default")
	}
}

func TestAllowUnauthenticatedAdminIsStrict(t *testing.T) {
	for _, value := range []string{"1", "yes", "TRUE", "True"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("TESLA_ALLOW_UNAUTHENTICATED_ADMIN", value)
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if bool(cfg.AllowUnauthenticatedAdmin) {
				t.Errorf("ALLOW_UNAUTHENTICATED_ADMIN=%q must not enable the bypass", value)
			}
		})
	}

	t.Setenv("TESLA_ALLOW_UNAUTHENTICATED_ADMIN", "true")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !bool(cfg.AllowUnauthenticatedAdmin) {
		t.Error(`ALLOW_UNAUTHENTICATED_ADMIN="true" must enable the bypass`)
	}
}

func TestOriginsParsing(t *testing.T) {
	cases := map[string][]string{
		"":  nil,
		"*": nil,
		" http://localhost:5173 , https://a.example ,, ": {"http://localhost:5173", "https://a.example"},
	}
	for raw, want := range cases {
		t.Run(raw, func(t *testing.T) {
			t.Setenv("TESLA_ALLOWED_ORIGINS", raw)
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if len(cfg.AllowedOrigins) != len(want) {
				t.Fatalf("got %v want %v", cfg.AllowedOrigins, want)
			}
			for i := range want {
				if cfg.AllowedOrigins[i] != want[i] {
					t.Fatalf("got %v want %v", cfg.AllowedOrigins, want)
				}
			}
		})
	}
}

func TestAdminPolicyAuthorization(t *testing.T) {
	withKey := AdminPolicy{APIKey: "s3cret"}

	reqWith := func(key string) *http.Request {
		r, _ := http.NewRequest(http.MethodPost, "/graphql", nil)
		if key != "" {
			r.Header.Set("X-Admin-Key", key)
		}
		return r
	}

	if !withKey.Authorized(reqWith("s3cret")) {
		t.Error("matching key must authorize")
	}
	if withKey.Authorized(reqWith("wrong")) {
		t.Error("wrong key must not authorize")
	}
	if withKey.Authorized(reqWith("")) {
		t.Error("missing key must not authorize")
	}
	if withKey.Authorized(nil) {
		t.Error("a nil request must not authorize")
	}
	if withKey.AllowUnauthenticated {
		t.Error("policy must not be open by construction")
	}

	// With no key configured, only the explicit local bypass grants access.
	closed := AdminPolicy{}
	if closed.Authorized(reqWith("anything")) {
		t.Error("no configured key must reject all keys")
	}
	open := AdminPolicy{AllowUnauthenticated: true}
	if !open.Authorized(nil) {
		t.Error("the bypass must authorize without a request")
	}
}
