// Command drbrain is the offline tool for the engine: validate and print the
// city, drive cars in a terminal, and run the pacing simulation.
//
// It exists so the engine is exercised through a real program and not only
// through tests. Three subcommands:
//
//	map    validate the city file and print it as ASCII glyph rows
//	play   drive cars manually, one tick at a time, in the terminal
//	sim    run the scripted bot and print a pacing table
//
// `sim` is the one to reach for when asking whether the game is balanced rather
// than whether it is correct: it reports the queue depth against what the fleet
// can handle. See the README for how to read its columns — two of the headings
// (`tgt`, `cls`) do not mean what their names suggest.
//
// Nothing here writes to a save file; `play` and `sim` build a world in memory
// and throw it away.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/wricardo/drbrain/games/delivery/game/engine"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	switch os.Args[1] {
	case "map":
		os.Exit(cmdMap(os.Args[2:]))
	case "play":
		os.Exit(cmdPlay(os.Args[2:]))
	case "sim":
		os.Exit(cmdSim(os.Args[2:]))
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `drbrain — DrBrain Delivery engine tools

usage:
  drbrain map  [-f maps/city.json] [-reveal-all] [-stats]
      Validate the city and print it as ASCII. Unexplored districts show '?'.

  drbrain play [city.json]
      Drive cars in the terminal with manual controls, one tick at a time.

  drbrain sim [-f city.json] [-ticks N] [-every N] [-seed N]
      Run a scripted bot and print a pacing table. Use it to answer
      "is the queue starved or drowning?" with numbers.

flags for map:
  -f           path to the city file (default maps/city.json)
  -reveal-all  reveal every district before printing
  -stats       print a reachability/capacity summary

ASCII legend:
  #  water      B  building   .  road      ,  ground
  D  depot      C  charger    K  kitchen   H  customer
  ?  unexplored               X  closed for street works
  1-9  car (by index, first nine only)
`)
}

func cmdMap(args []string) int {
	fs := flag.NewFlagSet("map", flag.ContinueOnError)
	path := fs.String("f", "maps/city.json", "path to city file")
	revealAll := fs.Bool("reveal-all", false, "reveal all districts")
	stats := fs.Bool("stats", false, "print reachability summary")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	city, cfg, err := engine.CityFromFile(*path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	fmt.Println(city.Summary())
	fmt.Println()

	if *revealAll {
		n := city.RevealAll()
		fmt.Printf("revealed %d hidden tiles\n\n", n)
	}

	cars := make([]engine.RenderCar, 0, len(cfg.InitialCars))
	for _, ic := range cfg.InitialCars {
		cars = append(cars, engine.RenderCar{Pos: ic.Pos})
	}
	fmt.Print(city.RenderASCII(cars))

	if *stats {
		fmt.Println()
		printStats(city, cfg)
	}
	return 0
}

func printStats(city *engine.City, cfg *engine.CityConfig) {
	fmt.Printf("hidden tiles: %d of %d\n", city.HiddenCount(), city.GridSize*city.GridSize)

	depot := cfg.Depots[0]
	reach := city.Reachable(depot.Pos)
	fmt.Printf("tiles reachable from %s: %d\n", depot.ID, len(reach))

	stranded := city.StrandedEntities()
	if len(stranded) == 0 {
		fmt.Println("stranded entities: none")
	} else {
		fmt.Printf("stranded entities: %v\n", stranded)
	}

	for _, k := range city.Kitchens {
		fmt.Printf("kitchen %-10s -> depot %3d steps\n", k.ID, city.Distance(depot.Pos, k.Pos))
	}
	for _, cu := range city.Customers {
		fmt.Printf("customer %-9s -> depot %3d steps\n", cu.ID, city.Distance(depot.Pos, cu.Pos))
	}
}
