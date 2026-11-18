package appconfig

import (
	"testing"
	"time"
)

func load(t *testing.T) Config {
	t.Helper()
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cfg
}

func TestDefaults(t *testing.T) {
	for _, key := range []string{
		"GRAPHQL_INTROSPECTION", "GRAPHQL_PLAYGROUND", "ADMIN_API_KEY",
		"ALLOW_UNAUTHENTICATED_ADMIN", "DATABASE_URL", "MAX_USER_MAPS",
		"MAX_SESSIONS", "MAX_ACCOUNTS", "SESSION_TTL", "SESSION_SWEEP_INTERVAL",
		"ALLOWED_ORIGINS", "AUTH_GOOGLE_CLIENT_ID", "AUTH_GOOGLE_CLIENT_SECRET",
		"AUTH_GITHUB_CLIENT_ID", "AUTH_GITHUB_CLIENT_SECRET", "AUTH_PASSWORD",
		"AUTH_DEV_FAKE",
	} {
		t.Setenv(key, "")
	}
	cfg := load(t)
	if !cfg.GraphQLIntrospection.Bool() || !cfg.GraphQLPlayground.Bool() {
		t.Fatalf("GraphQL flags = %v/%v, want both on", cfg.GraphQLIntrospection, cfg.GraphQLPlayground)
	}
	if cfg.AllowUnauthenticatedAdmin.Bool() {
		t.Fatal("built-in map writes are open by default")
	}
	if cfg.MaxUserMaps.Int() != DefaultMaxUserMaps || cfg.MaxSessions.Int() != DefaultMaxSessions || cfg.MaxAccounts.Int() != 10000 {
		t.Fatalf("caps = %d/%d/%d", cfg.MaxUserMaps.Int(), cfg.MaxSessions.Int(), cfg.MaxAccounts.Int())
	}
	if cfg.SessionTTL.Duration() != DefaultSessionTTL || cfg.SessionSweepInterval.Duration() != DefaultSweepInterval {
		t.Fatalf("durations = %s/%s", cfg.SessionTTL.Duration(), cfg.SessionSweepInterval.Duration())
	}
	if !cfg.AuthPassword.Bool() || cfg.AuthDevFake.Bool() {
		t.Fatalf("auth flags = %v/%v, want password on and dev fake off", cfg.AuthPassword, cfg.AuthDevFake)
	}
}

// TestFlagAcceptsTheSameSpellingsAsAuthFlags pins the contract the auth
// package already used: case and surrounding spaces must not change the answer,
// so GRAPHQL_INTROSPECTION=TRUE cannot silently turn introspection off.
func TestFlagAcceptsTheSameSpellingsAsAuthFlags(t *testing.T) {
	tests := []struct {
		name string
		val  string
		want bool
	}{
		{"unset keeps the default", "", true},
		{"lowercase true", "true", true},
		{"upper true", "TRUE", true},
		{"mixed true", "True", true},
		{"one", "1", true},
		{"yes", "yes", true},
		{"upper yes", "YES", true},
		{"on", "on", true},
		{"padded", "  true  ", true},
		{"false", "false", false},
		{"upper false", "FALSE", false},
		{"zero", "0", false},
		{"no", "no", false},
		{"unrecognised is off, not the default", "maybe", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GRAPHQL_INTROSPECTION", tc.val)
			if got := load(t).GraphQLIntrospection.Bool(); got != tc.want {
				t.Fatalf("GRAPHQL_INTROSPECTION=%q = %v, want %v", tc.val, got, tc.want)
			}
		})
	}
}

// TestStrictFlagOnlyAcceptsTrue keeps ALLOW_UNAUTHENTICATED_ADMIN honest: the
// documented contract is that only the exact value "true" opens built-in map
// writes, so a near miss must not.
func TestStrictFlagOnlyAcceptsTrue(t *testing.T) {
	for _, val := range []string{"", "TRUE", "1", "yes", "on", " false "} {
		t.Run(val, func(t *testing.T) {
			t.Setenv("ALLOW_UNAUTHENTICATED_ADMIN", val)
			if load(t).AllowUnauthenticatedAdmin.Bool() {
				t.Fatalf("ALLOW_UNAUTHENTICATED_ADMIN=%q was honoured", val)
			}
		})
	}
	t.Setenv("ALLOW_UNAUTHENTICATED_ADMIN", "true")
	if !load(t).AllowUnauthenticatedAdmin.Bool() {
		t.Fatal("ALLOW_UNAUTHENTICATED_ADMIN=true was ignored")
	}
}

// TestPasswordStaysOnUnlessTurnedOff pins AUTH_PASSWORD's asymmetry: it needs
// no registration anywhere, so only the documented off values turn it off.
func TestPasswordStaysOnUnlessTurnedOff(t *testing.T) {
	for _, val := range []string{"", "true", "TRUE", "1", "yes", "on", "off", "maybe"} {
		t.Run(val, func(t *testing.T) {
			t.Setenv("AUTH_PASSWORD", val)
			if !load(t).AuthPassword.Bool() {
				t.Fatalf("AUTH_PASSWORD=%q turned password sign-in off", val)
			}
		})
	}
	for _, val := range []string{"false", "FALSE", "0", "no"} {
		t.Run(val, func(t *testing.T) {
			t.Setenv("AUTH_PASSWORD", val)
			if load(t).AuthPassword.Bool() {
				t.Fatalf("AUTH_PASSWORD=%q did not turn password sign-in off", val)
			}
		})
	}
}

// TestPositiveIntRejectsNonPositive keeps the cap behaviour honest: a cap of 0
// or a negative would disable the limit, so an invalid value must fall back to
// the default rather than through.
func TestPositiveIntRejectsNonPositive(t *testing.T) {
	for _, val := range []string{"", "0", "-5", "abc"} {
		t.Run(val, func(t *testing.T) {
			t.Setenv("MAX_SESSIONS", val)
			if got := load(t).MaxSessions.Int(); got != DefaultMaxSessions {
				t.Fatalf("MAX_SESSIONS=%q = %d, want the default %d", val, got, DefaultMaxSessions)
			}
		})
	}
	t.Setenv("MAX_SESSIONS", "7")
	if got := load(t).MaxSessions.Int(); got != 7 {
		t.Fatalf("MAX_SESSIONS=7 = %d, want 7", got)
	}
}

func TestDurationFallsBackOnInvalidValues(t *testing.T) {
	for _, val := range []string{"", "soon", "-1h"} {
		t.Run(val, func(t *testing.T) {
			t.Setenv("SESSION_TTL", val)
			if got := load(t).SessionTTL.Duration(); got != DefaultSessionTTL {
				t.Fatalf("SESSION_TTL=%q = %s, want the default %s", val, got, DefaultSessionTTL)
			}
		})
	}
	t.Setenv("SESSION_TTL", "90m")
	if got := load(t).SessionTTL.Duration(); got != 90*time.Minute {
		t.Fatalf("SESSION_TTL=90m = %s, want 90m", got)
	}
}

func TestLoadTrimsCredentials(t *testing.T) {
	t.Setenv("DATABASE_URL", "  postgres://db/x  ")
	t.Setenv("AUTH_GOOGLE_CLIENT_ID", " google-id ")
	t.Setenv("AUTH_GITHUB_CLIENT_SECRET", " github-secret ")
	cfg := load(t)
	if cfg.DatabaseURL != "postgres://db/x" || cfg.AuthGoogleClientID != "google-id" || cfg.AuthGitHubClientSecret != "github-secret" {
		t.Fatalf("untrimmed: %q %q %q", cfg.DatabaseURL, cfg.AuthGoogleClientID, cfg.AuthGitHubClientSecret)
	}
}
