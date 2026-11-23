package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/wricardo/drbrain/games/delivery/game/engine"
)

// cmdPlay is the terminal driving loop. It is deliberately crude: the point is
// to exercise the engine's tick loop and manual movement through a real
// program before any HTTP or UI layer exists.
//
// Commands are line-based so it works in any terminal:
//
//	w a s d   queue a move for the active car (north/west/south/east)
//	tab       switch active car
//	t         advance one tick
//	ttt       advance three ticks
//	.         print car status
//	q         quit
//
// One design note, and the reason this CLI exists: queuing a move does NOT
// tick. You can queue moves for several cars and then advance once, which is
// exactly how the API will behave — commands set intent, the clock applies it.
func cmdPlay(args []string) int {
	path := "maps/city.json"
	if len(args) > 0 {
		path = args[0]
	}

	city, cfg, err := engine.CityFromFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	world, err := engine.NewWorld(city, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	fmt.Println("DrBrain Delivery — terminal driving")
	fmt.Println("w/a/s/d queue a move · tab switch car · t tick · ttt multi-tick · . status · q quit")

	active := 0
	scanner := bufio.NewScanner(os.Stdin)

	for {
		renderPlay(world, active)
		fmt.Print("> ")
		if !scanner.Scan() {
			return 0
		}
		line := strings.TrimSpace(scanner.Text())

		switch {
		case line == "q":
			fmt.Println("bye")
			return 0

		case line == ".":
			printCars(world, active)

		case line == "tab":
			active = (active + 1) % len(world.Cars)

		case line == "t" || (len(line) > 0 && strings.Trim(line, "t") == ""):
			// "t" or "ttt": advance len(line) ticks.
			for range len(line) {
				printEvents(world.Advance())
			}

		case line == "w" || line == "a" || line == "s" || line == "d":
			dir := map[string]engine.Direction{
				"w": engine.North, "s": engine.South,
				"a": engine.West, "d": engine.East,
			}[line]
			car := world.Cars[active]
			if err := world.QueueMove(car.ID, dir); err != nil {
				fmt.Printf("cannot queue: %v\n", err)
				continue
			}
			fmt.Printf("%s queued %s (%d pending)\n", car.Name, dir, len(car.PendingMoves()))

		case line == "":
			// ignore

		default:
			fmt.Printf("unknown command %q\n", line)
		}
	}
}

// renderPlay clears the screen, prints the map and the fleet.
func renderPlay(w *engine.World, active int) {
	fmt.Print("\033[H\033[2J")
	fmt.Printf("tick %-5d  cars %d  active: %s\n\n",
		w.Tick, len(w.Cars), w.Cars[active].Name)
	fmt.Print(w.City.RenderASCII(w.RenderCars()))
	fmt.Println()
	printCars(w, active)
}

// printCars prints one line per car, marking the active one.
func printCars(w *engine.World, active int) {
	for i, c := range w.Cars {
		mark := " "
		if i == active {
			mark = ">"
		}
		fmt.Printf("%s [%d] %-14s %-9s battery %3d/%3d (%3d%%)  at %s\n",
			mark, i, c.Name, c.State(w.City), c.Battery, c.Class.MaxBattery,
			c.BatteryPct(), c.Pos)
	}
	fmt.Println()
}

// printEvents prints the events a tick produced.
func printEvents(evs []engine.Event) {
	for _, e := range evs {
		fmt.Printf("  · %s\n", e.Message)
	}
}
