// Command drbrain-server serves the GraphQL API, and the web UI when one has
// been built.
//
// Startup order is deliberate: the city file is loaded and validated before the
// listener binds, so a bad map is a startup error rather than a runtime failure
// on the first request. Sessions are loaded from -sessions-dir on demand; a
// session appears in `sessions` whether or not it is in memory, and `loaded`
// says which.
//
// On SIGINT/SIGTERM it saves every live session before exiting, so a clean
// shutdown never loses progress. A hard kill does — there is no periodic
// autosave.
//
// If -ui-dir has no build, the root route is simply not mounted and the API is
// unaffected; the game is fully playable over GraphQL alone.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/wricardo/drbrain-delivery/api"
	"github.com/wricardo/drbrain-delivery/game/session"
	"github.com/wricardo/drbrain-delivery/graph"
)

func main() {
	os.Exit(run())
}

func run() int {
	var (
		cityFile    = flag.String("city", "maps/city.json", "path to the city file")
		sessionsDir = flag.String("sessions-dir", "sessions", "directory for session saves")
		addr        = flag.String("addr", "localhost:8080", "listen address")
		uiDir       = flag.String("ui-dir", "frontend/build", "directory of the built web UI (optional)")
	)
	flag.Parse()

	log.SetFlags(log.Ltime)

	mgr, err := session.NewManager(*cityFile, *sessionsDir)
	if err != nil {
		log.Printf("error: %v", err)
		return 1
	}

	// GraphQL is the only game transport. The web UI is an optional static
	// bundle served alongside it; without it the server is API-only, which is a
	// complete game.
	mux := http.NewServeMux()
	mux.Handle("/graphql", graph.Handler(mgr))
	mux.Handle("/graphql/playground", graph.Playground())

	if h, ok := api.StaticHandler(*uiDir); ok {
		log.Printf("serving web UI from %s", *uiDir)
		mux.Handle("/", h)
	} else {
		log.Printf("no web UI at %s (graphql only; run `make build-frontend` to add one)", *uiDir)
		log.Printf("query playground: http://%s/graphql/playground", *addr)
	}

	srv := &http.Server{
		Addr:              *addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	// Graceful shutdown: flush saves before exiting, or a restart silently
	// drops everything since the last explicit save.
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig

		log.Println("shutting down…")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			log.Printf("shutdown: %v", err)
		}
		if err := mgr.Close(); err != nil {
			log.Printf("save on shutdown: %v", err)
		}
	}()

	log.Printf("DrBrain Delivery listening on http://%s (graphql at /graphql)", *addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintf(os.Stderr, "listen: %v\n", err)
		return 1
	}
	<-stopped
	log.Println("stopped")
	return 0
}
