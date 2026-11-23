// Command rlenv exposes the Tesla game engine to a reinforcement-learning
// trainer over stdin/stdout. It speaks one JSON object per line.
//
//	-> {"cmd":"reset","map":"easy"}                      shipped map (name in -maps dir)
//	-> {"cmd":"reset","gen":{"size_min":8,...},"seed":7} fresh random map
//	-> {"cmd":"step","action":2}                         0=up 1=down 2=left 3=right
//	<- {"reward":..,"terminal":..,"mask":[..],...}\n  followed by NumPlanes*GridPad*GridPad raw bytes
//
// The engine is the real game engine: no re-implemented rules.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"

	"github.com/wricardo/drbrain/games/tesla/game/engine"
)

type request struct {
	Cmd    string     `json:"cmd"`
	Map    string     `json:"map"`
	Seed   *uint64    `json:"seed"`
	Gen    *GenParams `json:"gen"`
	Action int        `json:"action"`
	// used by "observe": a live Tesla_GameState plus the cells already driven over
	State   *remoteState `json:"state"`
	Visited [][2]int     `json:"visited"`
}

type response struct {
	Error     string  `json:"error,omitempty"`
	Reward    float64 `json:"reward"`
	Terminal  bool    `json:"terminal"`
	Truncated bool    `json:"truncated"`
	Reason    string  `json:"reason,omitempty"`
	Mask      [4]bool `json:"mask"`
	Battery   int     `json:"battery"`
	MaxBat    int     `json:"max_battery"`
	Parks     int     `json:"parks"`
	Left      int     `json:"left"`
	MoveLeft  int     `json:"move_left"`
	SincePark int     `json:"since_park"`
	Map       string  `json:"map"`
	Grid      int     `json:"grid"`
	BinPlanes int     `json:"binary_planes"` // planes 0..N-1 are 0/1, the rest are 0..255 bytes
	Planes    int     `json:"planes"`
	Size      int     `json:"size"`
}

func main() {
	mapsDir := flag.String("maps", "maps", "directory with shipped map JSON files")
	safety := flag.Float64("safety", 0.05,
		"weight of the battery-safety reward shaping (0 disables); see potential() in env.go")
	reject := flag.Bool("reject-illegal", false,
		"treat illegal moves as a wasted step instead of letting them crash (needed without action masking)")
	fog := flag.Int("fog", 0,
		"visibility radius in cells; 0 = whole map visible. With fog the car only sees cells it has visited, and unexplored cells are legal-but-risky moves")
	flag.Parse()

	e := newEnv(defaultRewards)
	e.fog = *fog
	e.safety = *safety
	e.reject = *reject
	rng := rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))
	obs := make([]byte, NumPlanes*GridPad*GridPad)
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 1<<20), 1<<20)
	out := bufio.NewWriterSize(os.Stdout, 1<<16)
	enc := json.NewEncoder(out)

	reply := func(r response, withObs bool) {
		if withObs {
			e.observe(obs)
			r.Planes, r.BinPlanes, r.Size, r.Grid = NumPlanes, numBinaryPlanes, len(obs), e.cfg.GridSize
			r.Map = e.cfg.Name
		}
		_ = enc.Encode(r)
		if withObs {
			_, _ = out.Write(obs)
		}
		_ = out.Flush()
	}

	for in.Scan() {
		var req request
		if err := json.Unmarshal(in.Bytes(), &req); err != nil {
			reply(response{Error: err.Error()}, false)
			continue
		}
		switch req.Cmd {
		case "reset":
			if req.Seed != nil {
				rng = rand.New(rand.NewPCG(*req.Seed, 0x9e3779b97f4a7c15))
			}
			cfg, err := pickMap(rng, *mapsDir, req)
			if err == nil {
				err = e.load(cfg)
			}
			if err != nil {
				reply(response{Error: err.Error()}, false)
				continue
			}
			var res stepResult
			e.fill(&res)
			reply(toResponse(res), true)
		case "observe":
			if req.State == nil {
				reply(response{Error: "observe needs \"state\""}, false)
				continue
			}
			if err := e.loadRemote(*req.State, req.Visited); err != nil {
				reply(response{Error: err.Error()}, false)
				continue
			}
			var res stepResult
			e.fill(&res)
			reply(toResponse(res), true)
		case "step":
			if e.eng == nil {
				reply(response{Error: "step before reset"}, false)
				continue
			}
			reply(toResponse(e.step(req.Action)), true)
		default:
			reply(response{Error: fmt.Sprintf("unknown cmd %q", req.Cmd)}, false)
		}
	}
	if err := in.Err(); err != nil && err != io.EOF {
		fmt.Fprintln(os.Stderr, "rlenv:", err)
		os.Exit(1)
	}
}

func toResponse(s stepResult) response {
	return response{
		Reward: s.Reward, Terminal: s.Terminal, Truncated: s.Truncated, Reason: s.Reason,
		Mask: s.Mask, Battery: s.Battery, MaxBat: s.MaxBat, Parks: s.Parks, Left: s.Left,
		MoveLeft: s.MoveLeft, SincePark: s.SincePark,
	}
}

func pickMap(rng *rand.Rand, dir string, req request) (*engine.GameConfig, error) {
	if req.Map != "" {
		data, err := os.ReadFile(filepath.Join(dir, req.Map+".json"))
		if err != nil {
			return nil, err
		}
		var cfg engine.GameConfig
		if err := json.Unmarshal(data, &cfg); err != nil {
			return nil, err
		}
		return &cfg, nil // structure is checked by NewEngineForLoad
	}
	if req.Gen == nil {
		return nil, fmt.Errorf("reset needs \"map\" or \"gen\"")
	}
	return generate(rng, *req.Gen)
}
