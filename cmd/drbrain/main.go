// Command drbrain is the single process for the Dr Brain games: one binary,
// one listener, path-routed games, one web app.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"

	"github.com/wricardo/drbrain/games/mazes"
	"github.com/wricardo/drbrain/games/motor"
	"github.com/wricardo/drbrain/platform"
	"github.com/wricardo/drbrain/platform/config"
	"github.com/wricardo/drbrain/platform/host"
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
	logger := newLogger(stderr, cfg.Debug)
	h := host.New(cfg, reg, logger)

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

func newLogger(w io.Writer, debug bool) *slog.Logger {
	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: level}))
}
