// Command import-json moves the game's runtime state between the JSON files
// (sessions/, accounts/, maps-user/) and PostgreSQL.
//
//	import-json                      copy the files into the database (safe to re-run)
//	import-json -dry-run             count what would be copied
//	import-json -export              write the database out as files (rollback copy)
//	import-json -migrate-only        apply the schema migrations and exit
//
// The database comes from -database-url or $DATABASE_URL. The file directories
// use the same flags and defaults as the server. An export refuses to write
// into directories that already hold .json files, so point it at fresh ones:
//
//	import-json -export -sessions-dir backup/sessions -accounts-dir backup/accounts -user-maps-dir backup/maps-user
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/joho/godotenv"

	"github.com/wricardo/drbrain/games/motor/internal/appconfig"
	"github.com/wricardo/drbrain/games/motor/internal/jsonmigrate"
	"github.com/wricardo/drbrain/platform/pgdb"
)

func main() {
	_ = godotenv.Load()
	var (
		dbURL    = flag.String("database-url", "", "PostgreSQL connection string (default $DATABASE_URL)")
		mapsDir  = flag.String("maps-dir", "maps", "directory with the shipped maps (tells them apart from player maps)")
		userMaps = flag.String("user-maps-dir", "maps-user", "directory with player-created maps")
		sessions = flag.String("sessions-dir", "sessions", "directory with persisted sessions")
		accounts = flag.String("accounts-dir", "accounts", "directory with accounts/ and logins/")
		export   = flag.Bool("export", false, "write the database out as files instead of importing")
		dryRun   = flag.Bool("dry-run", false, "import: count what would be copied, change nothing")
		migrate  = flag.Bool("migrate-only", false, "apply the schema migrations and exit")
	)
	flag.Parse()

	url := strings.TrimSpace(*dbURL)
	if url == "" {
		cfg, err := appconfig.Load()
		if err != nil {
			log.Fatalf("import-json: configuration: %v", err)
		}
		url = cfg.DatabaseURL
	}
	if url == "" {
		log.Fatal("import-json: set -database-url or DATABASE_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	pool, err := pgdb.Open(ctx, url)
	if err != nil {
		log.Fatalf("import-json: %v", err)
	}
	defer pool.Close()

	if *migrate {
		fmt.Println("schema is up to date")
		return
	}

	dirs := jsonmigrate.Dirs{Maps: *mapsDir, UserMaps: *userMaps, Sessions: *sessions, Accounts: *accounts}
	var counts jsonmigrate.Counts
	verb := "imported"
	switch {
	case *export:
		verb = "exported"
		counts, err = jsonmigrate.Export(ctx, pool, dirs)
	default:
		if *dryRun {
			verb = "would import"
		}
		counts, err = jsonmigrate.Import(ctx, pool, dirs, *dryRun)
	}
	if err != nil {
		log.Fatalf("import-json: %v", err)
	}
	fmt.Printf("%s: %s\n", verb, counts)
}
