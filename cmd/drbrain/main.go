// Command drbrain is the single process for the Dr Brain games: one binary,
// one listener, path-routed games, one web app.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"

	"github.com/wricardo/drbrain/games/delivery"
	deliverysession "github.com/wricardo/drbrain/games/delivery/game/session"
	deliverygraph "github.com/wricardo/drbrain/games/delivery/graph"
	"github.com/wricardo/drbrain/games/mazes"
	mazesgraph "github.com/wricardo/drbrain/games/mazes/graph"
	"github.com/wricardo/drbrain/games/motor"
	motorgraph "github.com/wricardo/drbrain/games/motor/graph"
	"github.com/wricardo/drbrain/games/music"
	musicgraph "github.com/wricardo/drbrain/games/music/graph"
	"github.com/wricardo/drbrain/games/tesla"
	teslagraph "github.com/wricardo/drbrain/games/tesla/graph"
	"github.com/wricardo/drbrain/graph"
	"github.com/wricardo/drbrain/platform"
	"github.com/wricardo/drbrain/platform/account"
	accountgraph "github.com/wricardo/drbrain/platform/account/graph"
	"github.com/wricardo/drbrain/platform/auth"
	"github.com/wricardo/drbrain/platform/config"
	"github.com/wricardo/drbrain/platform/host"
	"github.com/wricardo/drbrain/platform/pgdb"
)

func main() {
	os.Exit(run(os.Args[1:], builtinGames(), os.Stdout, os.Stderr))
}

// builtinGames is the compiled-in registry. Games are appended here as they
// migrate to platform.Game.
func builtinGames() []platform.Game {
	return []platform.Game{
		mazes.New(),
		motor.New(),
		delivery.New(),
		tesla.New(),
		music.New(),
	}
}

// builtinGraphQL wires the one executor. Enabled games hand over their
// resolver roots; a disabled game leaves its namespace nil and the adapters
// report it as unavailable.
func builtinGraphQL(cfg config.Config) host.GraphQLBuilder {
	return func(b host.GraphQLBindings) (http.Handler, error) {
		delivery, _ := b.Resolvers["delivery"].(*deliverygraph.Resolver)
		mazes, _ := b.Resolvers["mazes"].(*mazesgraph.Resolver)
		motor, _ := b.Resolvers["motor"].(*motorgraph.Resolver)
		tesla, _ := b.Resolvers["tesla"].(*teslagraph.Resolver)
		music, _ := b.Resolvers["music"].(*musicgraph.Resolver)
		accountResolver := accountgraph.New(b.Accounts, b.Authn)
		mux := http.NewServeMux()
		mux.Handle("/graphql", graph.NewHandler(graph.Options{
			Resolver:       graph.New(accountResolver, delivery, mazes, motor, tesla, music),
			AllowedOrigins: cfg.AllowedOrigins,
			Introspection:  true,
			Playground:     true,
			Auth:           b.Authn,
		}))
		mux.Handle("/graphql/playground", graph.Playground())
		return mux, nil
	}
}

func run(args []string, games []platform.Game, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	switch args[0] {
	case "serve":
		return runServe(args[1:], games, stdout, stderr)
	case "games":
		return runGames(args[1:], games, stdout, stderr)
	case "validate":
		return runGameCommand("validate", args[1:], games, stdout, stderr)
	case "mapgen":
		return runGameCommand("mapgen", args[1:], games, stdout, stderr)
	case "import-maps":
		return runImportMaps(args[1:], stdout, stderr)
	case "backfill-delivery-history":
		return runBackfillDeliveryHistory(args[1:], stdout, stderr)
	case "stdio-mcp", "mcp-stdio", "mcp":
		fmt.Fprintln(stderr, "drbrain: stdio MCP is disabled: the MCP transport runs over HTTP at /<game>/mcp (set TESLA_MCP_ENABLED=true)")
		return 1
	case "help", "-h", "--help":
		usage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "drbrain: unknown command %q\n", args[0])
		usage(stderr)
		return 2
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `usage: drbrain <command> [flags]

commands:
  serve                 serve the enabled games on one listener
  games list            print the compiled-in game registry
  validate --game=NAME  validate a game's maps and config
  mapgen   --game=NAME  generate a map for a game
  import-maps           import legacy Tesla maps / Delivery cities
  backfill-delivery-history  rebuild delivery history tables from saves

import-maps: see "drbrain import-maps -h" for flags and cutover order.
backfill-delivery-history: see "drbrain backfill-delivery-history -h".

serve flags:
  --games=a,b           override DRBRAIN_GAMES
`)
}

func runServe(args []string, games []platform.Game, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	selected := fs.String("games", "", "comma-separated game list (overrides DRBRAIN_GAMES)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "drbrain serve: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "drbrain serve: %v\n", err)
		return 1
	}
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "games" {
			cfg.Games = *selected
		}
	})
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(stderr, "drbrain serve: %v\n", err)
		return 1
	}
	reg, err := host.NewRegistry(games...)
	if err != nil {
		fmt.Fprintf(stderr, "drbrain serve: %v\n", err)
		return 1
	}
	// Resolve the selection now: a typo in --games or DRBRAIN_GAMES fails
	// before we open the database or bind anything.
	if _, err := reg.Select(cfg.EnabledGames()); err != nil {
		fmt.Fprintf(stderr, "drbrain serve: %v\n", err)
		return 1
	}
	logger := newLogger(stderr, cfg.Debug)

	pool, err := pgdb.Open(context.Background(), cfg.DatabaseURL)
	if err != nil {
		fmt.Fprintf(stderr, "drbrain serve: database: %v\n", err)
		return 1
	}
	defer pool.Close()

	accounts := account.New(account.Options{
		Store:          account.NewPostgresStore(pool),
		PasswordSignIn: cfg.AuthPassword,
	})
	providers, err := auth.Providers(auth.ProvidersConfig{
		GoogleID:     cfg.GoogleClientID,
		GoogleSecret: cfg.GoogleClientSecret,
		GitHubID:     cfg.GitHubClientID,
		GitHubSecret: cfg.GitHubClientSecret,
		DevFake:      cfg.AuthDevFake,
	}, cfg.PublicURL)
	if err != nil {
		fmt.Fprintf(stderr, "drbrain serve: sign-in: %v\n", err)
		return 1
	}
	var authn *auth.Auth
	if len(providers) > 0 || cfg.AuthPassword {
		authn, err = auth.New(auth.Config{
			Providers: providers, Accounts: accounts, PublicURL: cfg.PublicURL,
			AllowedOrigins: cfg.AllowedOrigins, Password: cfg.AuthPassword,
			JWTSecret: cfg.AuthJWTSecret,
		})
		if err != nil {
			fmt.Fprintf(stderr, "drbrain serve: sign-in: %v\n", err)
			return 1
		}
	}

	h := host.New(cfg, reg, logger,
		host.WithGraphQL(builtinGraphQL(cfg)),
		host.WithDB(pool),
		host.WithServices(accounts, authn),
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := h.Run(ctx); err != nil {
		fmt.Fprintf(stderr, "drbrain serve: %v\n", err)
		return 1
	}
	return 0
}

func runGames(args []string, games []platform.Game, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] != "list" {
		fmt.Fprintf(stderr, "drbrain games: unknown subcommand %q (want list)\n", args[0])
		return 2
	}
	reg, err := host.NewRegistry(games...)
	if err != nil {
		fmt.Fprintf(stderr, "drbrain games: %v\n", err)
		return 1
	}
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tPREFIX\tAPI")
	for _, g := range reg.Games() {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", g.Name(), g.Prefix(), strings.Join(g.API(), ","))
	}
	_ = tw.Flush()
	if len(reg.Games()) == 0 {
		fmt.Fprintln(stdout, "(no games compiled in)")
	}
	return 0
}

// runGameCommand dispatches validate/mapgen to a game that implements the
// matching optional interface.
func runGameCommand(command string, args []string, games []platform.Game, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(stderr)
	name := fs.String("game", "", "game name")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *name == "" {
		fmt.Fprintf(stderr, "drbrain %s: --game is required\n", command)
		return 2
	}
	reg, err := host.NewRegistry(games...)
	if err != nil {
		fmt.Fprintf(stderr, "drbrain %s: %v\n", command, err)
		return 1
	}
	g, ok := reg.Lookup(*name)
	if !ok {
		fmt.Fprintf(stderr, "drbrain %s: unknown game %q (compiled in: %v)\n", command, *name, reg.Names())
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	rest := fs.Args()
	switch command {
	case "validate":
		v, ok := g.(platform.Validator)
		if !ok {
			fmt.Fprintf(stderr, "drbrain validate: game %q does not implement validate\n", *name)
			return 1
		}
		err = v.Validate(ctx)
	case "mapgen":
		m, ok := g.(platform.MapGenerator)
		if !ok {
			fmt.Fprintf(stderr, "drbrain mapgen: game %q does not implement mapgen\n", *name)
			return 1
		}
		err = m.Mapgen(ctx, rest)
	}
	if err != nil {
		fmt.Fprintf(stderr, "drbrain %s: %v\n", command, err)
		return 1
	}
	return 0
}

// runBackfillDeliveryHistory rebuilds the indexed delivery history tables from
// platform_sessions. It is idempotent and safe to rerun after a failed
// projection.
func runBackfillDeliveryHistory(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("backfill-delivery-history", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dryRun := fs.Bool("dry-run", false, "decode and validate saves without writing projections")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "drbrain backfill-delivery-history: unexpected argument %q\n", fs.Arg(0))
		return 2
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "drbrain backfill-delivery-history: %v\n", err)
		return 1
	}
	pool, err := pgdb.Open(context.Background(), cfg.DatabaseURL)
	if err != nil {
		fmt.Fprintf(stderr, "drbrain backfill-delivery-history: database: %v\n", err)
		return 1
	}
	defer pool.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	written, err := deliverysession.BackfillHistory(ctx, pool, deliverysession.BackfillOptions{DryRun: *dryRun})
	if err != nil {
		fmt.Fprintf(stderr, "drbrain backfill-delivery-history: projected %d: %v\n", written, err)
		return 1
	}
	if *dryRun {
		fmt.Fprintf(stdout, "validated %d delivery sessions (dry run)\n", written)
		return 0
	}
	fmt.Fprintf(stdout, "projected %d delivery sessions\n", written)
	return 0
}

func newLogger(w io.Writer, debug bool) *slog.Logger {
	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: level}))
}
