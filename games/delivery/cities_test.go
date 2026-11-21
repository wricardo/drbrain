package delivery

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wricardo/drbrain/platform"
)

func writeCity(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	// resolveCities/loadEnv do not parse content; catalogue tests own that.
	if err := os.WriteFile(path, []byte(`{"name":"x"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestResolveCitiesLegacyFile(t *testing.T) {
	dir := t.TempDir()
	path := writeCity(t, dir, "town.json")

	setup, err := resolveCities(Env{City: path})
	if err != nil {
		t.Fatal(err)
	}
	if setup.BuiltinDir != dir || setup.DefaultID != "town" || setup.DefaultFile != path {
		t.Fatalf("setup = %+v", setup)
	}
}

func TestResolveCitiesDirDefaultsToCityJSON(t *testing.T) {
	dir := t.TempDir()
	want := writeCity(t, dir, "city.json")

	t.Setenv("DELIVERY_CITY", "")
	t.Setenv("DELIVERY_CITIES_DIR", dir)
	env, err := loadEnv()
	if err != nil {
		t.Fatal(err)
	}
	setup, err := resolveCities(env)
	if err != nil {
		t.Fatal(err)
	}
	if setup.BuiltinDir != dir || setup.DefaultID != "city" || setup.DefaultFile != want {
		t.Fatalf("setup = %+v", setup)
	}
}

func TestResolveCitiesDirUsesExplicitDefaultFile(t *testing.T) {
	dir := t.TempDir()
	path := writeCity(t, dir, "metro.json")

	t.Setenv("DELIVERY_CITY", path)
	t.Setenv("DELIVERY_CITIES_DIR", dir)
	env, err := loadEnv()
	if err != nil {
		t.Fatal(err)
	}
	setup, err := resolveCities(env)
	if err != nil {
		t.Fatal(err)
	}
	if setup.DefaultID != "metro" || setup.DefaultFile != path {
		t.Fatalf("setup = %+v", setup)
	}
}

func TestResolveCitiesRejectsDefaultOutsideDir(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	path := writeCity(t, outside, "city.json")

	t.Setenv("DELIVERY_CITY", path)
	t.Setenv("DELIVERY_CITIES_DIR", dir)
	env, err := loadEnv()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolveCities(env); err == nil || !strings.Contains(err.Error(), "DELIVERY_CITIES_DIR") {
		t.Fatalf("error = %v", err)
	}
}

// A broken builtin inside CITIES_DIR must fail Mount before the missing
// database is reported: the city catalogue is validated first.
func TestMalformedCITIESDIRFailsBeforeDatabaseCheck(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "city.json")
	if err := os.WriteFile(bad, []byte(`{"name":"broken","layout":[`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DELIVERY_CITIES_DIR", dir)
	t.Setenv("DELIVERY_CITY", "")

	g := New()
	err := g.Mount(nil, platform.Deps{})
	if err == nil {
		t.Fatal("Mount succeeded with a malformed city")
	}
	if !strings.Contains(err.Error(), "city.json") {
		t.Fatalf("error %q does not name the broken city", err)
	}
}
