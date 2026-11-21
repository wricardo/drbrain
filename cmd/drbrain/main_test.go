package main

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/wricardo/drbrain/platform"
)

type cliGame struct {
	name      string
	prefix    string
	validated bool
}

func (g *cliGame) Name() string   { return g.name }
func (g *cliGame) Prefix() string { return g.prefix }
func (g *cliGame) API() []string  { return []string{"/graphql"} }

func (g *cliGame) Mount(*http.ServeMux, platform.Deps) error { return nil }
func (g *cliGame) Shutdown(context.Context) error            { return nil }

type validatingGame struct {
	*cliGame
	validateErr error
}

func (g *validatingGame) Validate(context.Context) error { return g.validateErr }

func TestGamesListPrintsRegistry(t *testing.T) {
	games := []platform.Game{
		&cliGame{name: "mazes", prefix: "/mazes"},
		&cliGame{name: "motor", prefix: "/motor"},
	}
	var out, errOut bytes.Buffer
	code := run([]string{"games", "list"}, games, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, errOut.String())
	}
	for _, want := range []string{"NAME", "mazes", "/mazes", "motor", "/motor", "/graphql"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %q:\n%s", want, out.String())
		}
	}
}

func TestServeUnknownGameFailsBeforeBinding(t *testing.T) {
	addr := freePort(t)
	t.Setenv("DRBRAIN_ADDR", addr)
	t.Setenv("DRBRAIN_GAMES", "")
	t.Setenv("DRBRAIN_DATABASE_URL", "postgres://drbrain@127.0.0.1:1/drbrain")
	var out, errOut bytes.Buffer
	code := run([]string{"serve", "--games=unknown"}, nil, &out, &errOut)
	if code == 0 {
		t.Fatalf("exit = 0, want non-zero (stderr %q)", errOut.String())
	}
	if !strings.Contains(errOut.String(), "unknown game") {
		t.Errorf("stderr = %q, want unknown game", errOut.String())
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("port %s was bound: %v", addr, err)
	}
	ln.Close()
}

func TestValidateDispatchesToGame(t *testing.T) {
	games := []platform.Game{
		&validatingGame{cliGame: &cliGame{name: "mazes", prefix: "/mazes"}},
		&cliGame{name: "misc", prefix: "/misc"},
	}
	var out, errOut bytes.Buffer
	if code := run([]string{"validate", "--game=mazes"}, games, &out, &errOut); code != 0 {
		t.Fatalf("implemented validate exit = %d, stderr = %q", code, errOut.String())
	}

	errOut.Reset()
	if code := run([]string{"validate", "--game=misc"}, games, &out, &errOut); code == 0 {
		t.Fatal("validate of a non-implementing game succeeded")
	}
	if !strings.Contains(errOut.String(), "does not implement validate") {
		t.Errorf("stderr = %q", errOut.String())
	}

	errOut.Reset()
	if code := run([]string{"validate", "--game=nope"}, games, &out, &errOut); code == 0 {
		t.Fatal("validate of an unknown game succeeded")
	}
	if !strings.Contains(errOut.String(), "unknown game") {
		t.Errorf("stderr = %q", errOut.String())
	}
}

func TestUnknownCommandExitsTwo(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"bogus"}, nil, &out, &errOut); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
}

func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}
