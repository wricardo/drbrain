// Command drbrain-motor-programming serves Dr Brain - Motor Programming, the robot puzzle: GraphQL API with
// websocket subscriptions, /llms.txt and the SvelteKit UI.
package main

import (
	"context"
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"github.com/wricardo/drbrain-motor-programming/api"
	"github.com/wricardo/drbrain-motor-programming/auth"
	"github.com/wricardo/drbrain-motor-programming/game/account"
	"github.com/wricardo/drbrain-motor-programming/game/config"
	"github.com/wricardo/drbrain-motor-programming/game/service"
	"github.com/wricardo/drbrain-motor-programming/game/session"
	"github.com/wricardo/drbrain-motor-programming/transport/websocket"
)

const (
	appName = "drbrain-motor-programming"

	defaultMaxSessions = 1000
	// defaultMaxUserMaps caps player-created maps (MAX_USER_MAPS).
	defaultMaxUserMaps = 200
	// defaultMaxAccounts caps player accounts (MAX_ACCOUNTS).
	defaultMaxAccounts = service.DefaultMaxAccounts
	shutdownTimeout    = 10 * time.Second
)

//go:embed llms.txt.tmpl
var llmsTxtSource string

var (
	port         = flag.Int("port", 8000, "HTTP server port")
	host         = flag.String("host", "0.0.0.0", "HTTP server host")
	mapsDir      = flag.String("maps-dir", "maps", "Directory containing map JSON files")
	userMapsDir  = flag.String("user-maps-dir", "maps-user", "Directory for maps created through the API (survives deploys)")
	sessionsDir  = flag.String("sessions-dir", "sessions", "Directory for persisted sessions")
	accountsDir  = flag.String("accounts-dir", "accounts", "Directory for player accounts and logins (survives deploys); used unless AUTH_PASSWORD=false and no OAuth provider is configured")
	solutionsDir = flag.String("solutions-dir", "solutions", "Directory containing reference solutions (used by `just validate`; never served)")
	debug        = flag.Bool("debug", false, "Enable debug logging")
	publicURL    = flag.String("public-url", "", "Public base URL rendered into /llms.txt (defaults to the request host)")
)

// envBool reads a boolean env var. Unset returns def. It is deliberately as
// forgiving as the auth flags (which lowercase and trim before comparing), so
// GRAPHQL_INTROSPECTION=TRUE cannot silently mean false: an operator who
// capitalises a flag must not turn a feature off by accident. Recognised true
// values are true/1/yes/on; unset returns the default, anything else is false.
func envBool(key string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "":
		return def
	case "true", "1", "yes", "on":
		return true
	}
	return false
}

// envInt reads a positive integer env var; unset or invalid returns def.
func envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		log.Printf("ignoring invalid %s=%q, using %d", key, v, def)
		return def
	}
	return n
}

// envDuration reads a Go duration env var (e.g. "168h"); unset or invalid
// returns def.
func envDuration(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < 0 {
		log.Printf("ignoring invalid %s=%q, using %s", key, v, def)
		return def
	}
	return d
}

// dirExists reports whether path is an existing directory.
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func main() {
	if err := godotenv.Load(); err != nil {
		if !os.IsNotExist(err) {
			log.Printf("warning: loading .env: %v", err)
		}
	} else {
		log.Println("loaded environment variables from .env")
	}
	flag.Parse()

	if *debug {
		log.SetFlags(log.LstdFlags | log.Lshortfile)
	}
	if err := run(); err != nil {
		log.Fatalf("%s: %v", appName, err)
	}
}

// run wires the application and blocks until SIGINT/SIGTERM.
func run() error {
	maps, err := config.NewManager(config.Options{
		Dir:         *mapsDir,
		UserDir:     *userMapsDir,
		MaxUserMaps: envInt("MAX_USER_MAPS", defaultMaxUserMaps),
	})
	if err != nil {
		return fmt.Errorf("load maps: %w", err)
	}
	persistence, err := session.NewFilePersistence(*sessionsDir)
	if err != nil {
		return fmt.Errorf("session persistence: %w", err)
	}
	sessions := session.NewManager(persistence, envInt("MAX_SESSIONS", defaultMaxSessions))
	if err := sessions.LoadAll(); err != nil {
		log.Printf("warning: loading persisted sessions: %v", err)
	}

	// Sign-in: username/password accounts are on by default (they need nothing
	// registered anywhere; AUTH_PASSWORD=false turns them off) and the OAuth
	// providers are each on when both their id and secret are set. With neither,
	// nothing below is created, no accounts/ directory is made, and the server
	// behaves exactly as before.
	providers, err := auth.ProvidersFromEnv(os.Getenv, *publicURL)
	if err != nil {
		return fmt.Errorf("sign-in configuration: %w", err)
	}
	passwordSignIn := auth.PasswordFromEnv(os.Getenv)
	var accounts *account.Store
	if len(providers) > 0 || passwordSignIn || dirExists(*accountsDir) {
		accounts, err = account.Open(*accountsDir, nil)
		if err != nil {
			return fmt.Errorf("accounts: %w", err)
		}
	}

	hub := websocket.NewHub()
	svcOpts := service.Options{
		SessionTTL:     envDuration("SESSION_TTL", 7*24*time.Hour),
		MaxAccounts:    envInt("MAX_ACCOUNTS", defaultMaxAccounts),
		PasswordSignIn: passwordSignIn,
	}
	if accounts != nil {
		svcOpts.Accounts = accounts
	}
	svc := service.New(maps, sessions, hub, svcOpts)

	var authHandler *auth.Auth
	if len(providers) > 0 || passwordSignIn {
		authHandler, err = auth.New(auth.Config{
			Providers: providers, Accounts: svc, PublicURL: *publicURL, AllowedOrigins: os.Getenv("ALLOWED_ORIGINS"),
			Password: passwordSignIn,
		})
		if err != nil {
			return fmt.Errorf("sign-in: %w", err)
		}
		names := make([]string, len(providers))
		for i, p := range providers {
			names[i] = p.ID()
		}
		log.Printf("sign-in enabled: providers=%v password=%t (accounts=%s)", names, passwordSignIn, *accountsDir)
		if passwordSignIn && *publicURL == "" {
			log.Printf("warning: no -public-url: sign-in requests are only accepted on localhost hosts or origins listed in ALLOWED_ORIGINS. Set -public-url to the site's base URL in production.")
		}
	}

	introspection := envBool("GRAPHQL_INTROSPECTION", true)
	playground := envBool("GRAPHQL_PLAYGROUND", true)
	log.Printf("GraphQL introspection: %t, playground: %t", introspection, playground)

	apiServer, err := api.NewServer(svc, hub, api.Options{
		Introspection:  introspection,
		Playground:     playground,
		AllowedOrigins: os.Getenv("ALLOWED_ORIGINS"),
		PublicURL:      *publicURL,
		LLMSTemplate:   llmsTxtSource,
		Auth:           authHandler,
	})
	if err != nil {
		return err
	}

	// net.JoinHostPort brackets an IPv6 literal: -host ::1 must become [::1]:8000,
	// not the unparseable ::1:8000 that Sprintf would produce.
	addr := net.JoinHostPort(*host, strconv.Itoa(*port))
	httpServer := &http.Server{
		Addr:              addr,
		Handler:           apiServer,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serveErr := make(chan error, 1)
	go func() {
		log.Printf("listening on %s (maps=%s user-maps=%s sessions=%s solutions=%s)", addr, *mapsDir, *userMapsDir, *sessionsDir, *solutionsDir)
		log.Printf("GraphQL: http://%s/graphql  llms.txt: http://%s/llms.txt", addr, addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	select {
	case sig := <-stop:
		log.Printf("received %v, shutting down", sig)
	case err := <-serveErr:
		svc.Shutdown()
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		log.Printf("http shutdown: %v", err)
	}
	svc.Shutdown()
	log.Println("server stopped")
	return nil
}
