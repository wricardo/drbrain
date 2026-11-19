// Command validate checks every map in -maps-dir (structure, treat
// reachability) against its reference solution in -solutions-dir.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/wricardo/drbrain/games/motor/game/engine"
	"github.com/wricardo/drbrain/games/motor/validate"
)

func main() {
	mapsDir := flag.String("maps-dir", "maps", "directory with map JSON files")
	solDir := flag.String("solutions-dir", "solutions", "directory with <mapID>.json reference programs")
	flag.Parse()
	// A clone without the private solutions must not look like 17 broken maps.
	// Without them the structural and reachability checks still run, the solution
	// checks are reported as skipped, and the exit code says "nothing failed".
	withSolutions := haveSolutions(*solDir)
	if !withSolutions {
		fmt.Printf("SKIP no reference solutions in %s: map structure and treat reachability only\n", *solDir)
	}
	if checkAll(*mapsDir, *solDir, withSolutions) {
		os.Exit(1)
	}
}

// haveSolutions reports whether dir holds at least one <mapID>.json.
func haveSolutions(dir string) bool {
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	return err == nil && len(files) > 0
}

// checkAll prints one line per map and reports whether any check failed.
func checkAll(mapsDir, solDir string, withSolutions bool) (failed bool) {
	files, err := filepath.Glob(filepath.Join(mapsDir, "*.json"))
	if err != nil || len(files) == 0 {
		fmt.Fprintf(os.Stderr, "FAIL no maps found in %s\n", mapsDir)
		return true
	}
	sort.Strings(files)
	for _, f := range files {
		id := strings.TrimSuffix(filepath.Base(f), ".json")
		if err := checkOne(f, id, solDir, withSolutions); err != nil {
			fmt.Printf("FAIL %-20s %v\n", id, err)
			failed = true
		}
	}
	return failed
}

func checkOne(file, id, solDir string, withSolutions bool) error {
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	var cfg engine.MapConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return err
	}
	if cfg.ID != id {
		return fmt.Errorf("map id %q does not match file name", cfg.ID)
	}
	if res := validate.Check(cfg); !res.Valid {
		return fmt.Errorf("invalid map: %s", strings.Join(res.Issues, "; "))
	}
	m, err := engine.NewMap(cfg)
	if err != nil {
		return err
	}
	if !withSolutions {
		// Structure and reachability are validated above; the solution checks are
		// what cannot run without the private answer key.
		fmt.Printf("OK   %-20s valid map, solution checks skipped\n", id)
		return nil
	}
	p, err := validate.LoadSolution(solDir, id)
	if err != nil {
		return err
	}
	if err := validate.CheckSolution(m, p); err != nil {
		return err
	}
	if err := validate.CheckRecursionRequired(m, p); err != nil {
		return err
	}
	st := engine.Run(m, p)
	note := ""
	if m.AllowRecursion {
		note = " (recursion required)"
	}
	fmt.Printf("OK   %-20s WON in %d steps%s\n", id, st.Steps, note)
	return nil
}
