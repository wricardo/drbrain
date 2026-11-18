package main

import (
	"reflect"
	"testing"

	"github.com/wricardo/drbrain-motor-programming/game/engine"
	"github.com/wricardo/drbrain-motor-programming/validate"
)

func TestGenerateProducesSolvableMap(t *testing.T) {
	cases := []struct {
		name   string
		size   int
		seed   int64
		treats int
	}{
		{"5x5 seed 1", 5, 1, 0},
		{"5x5 seed 2", 5, 2, 0},
		{"5x5 seed 7 two treats", 5, 7, 2},
		{"7x7 seed 3", 7, 3, 0},
		{"20x20 seed 1", 20, 1, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, prog, err := Generate(tc.size, tc.seed, tc.treats, "gen_test", "Generated test")
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			if res := validate.Check(cfg); !res.Valid {
				t.Fatalf("map is invalid: %v", res.Issues)
			}
			m, err := engine.NewMap(cfg)
			if err != nil {
				t.Fatalf("NewMap: %v", err)
			}
			if err := validate.CheckSolution(m, prog); err != nil {
				t.Fatalf("reference solution: %v", err)
			}
			if got := len(cfg.Layout); got != tc.size {
				t.Fatalf("layout has %d rows, want %d", got, tc.size)
			}
			if cfg.MainTapeLength != len(prog.Main) {
				t.Fatalf("main_tape_length %d != program length %d", cfg.MainTapeLength, len(prog.Main))
			}
			if len(cfg.SubTapeLengths) == 0 {
				t.Fatal("generated map has no subs")
			}
			if len(cfg.SubTapeLengths) > engine.MaxSubs {
				t.Fatalf("%d subs, want at most %d", len(cfg.SubTapeLengths), engine.MaxSubs)
			}
			if len(prog.Subs) != len(cfg.SubTapeLengths) {
				t.Fatalf("program has %d subs, map declares %d", len(prog.Subs), len(cfg.SubTapeLengths))
			}
			for i, l := range cfg.SubTapeLengths {
				if l < 1 || l > engine.MaxSubTapeLength {
					t.Fatalf("sub %d has %d slots, want 1..%d", i+1, l, engine.MaxSubTapeLength)
				}
			}
			// The run must not fit on the main tape, or a program without subs
			// could win and the sub would be decorative.
			actions, ok := shortestProgram(m)
			if !ok {
				t.Fatal("no literal run found")
			}
			if cfg.MainTapeLength >= len(actions) {
				t.Fatalf("main tape %d slots >= %d-action run: a flat program still fits", cfg.MainTapeLength, len(actions))
			}
			if cfg.MaxSteps <= len(actions) {
				t.Fatalf("max_steps %d does not cover the %d-action run", cfg.MaxSteps, len(actions))
			}
			if tc.treats > 0 && countTreats(cfg.Layout) != tc.treats {
				t.Fatalf("map has %d treats, want %d", countTreats(cfg.Layout), tc.treats)
			}
		})
	}
}

func TestGenerateAlwaysFindsSubs(t *testing.T) {
	for _, size := range []int{5, 7, 9, 20} {
		for seed := int64(1); seed <= 10; seed++ {
			cfg, _, err := Generate(size, seed, 0, "gen_test", "Generated test")
			if err != nil {
				t.Fatalf("size %d seed %d: %v", size, seed, err)
			}
			if len(cfg.SubTapeLengths) == 0 {
				t.Fatalf("size %d seed %d: no subs", size, seed)
			}
		}
	}
}

func TestDecomposeFactorsRepeatedBlock(t *testing.T) {
	f := engine.MoveForward
	tr := engine.TurnRight
	// Four identical square sides. Greedy takes the block that saves the most
	// main slots: the 4-slot half called twice (saves 6) over the 2-slot side
	// called four times (saves 4).
	main, subs, calls := decompose([]engine.Instruction{tr, f, tr, f, tr, f, tr, f})
	if len(subs) != 1 || len(subs[0]) != 4 {
		t.Fatalf("subs = %v, want one 4-slot sub", subs)
	}
	if len(main) != 2 || calls != 2 {
		t.Fatalf("main = %v with %d calls, want 2 calls", main, calls)
	}
	for _, ins := range main {
		if ins != engine.CallSub1 {
			t.Fatalf("main holds %q, want CALL_SUB_1", ins)
		}
	}

	// No block of length >= 2 repeats contiguously: nothing to factor.
	main, subs, calls = decompose([]engine.Instruction{f, f, f, tr, tr, tr})
	if len(subs) != 0 || calls != 0 || len(main) != 6 {
		t.Fatalf("decompose of an aperiodic run = %v/%v/%d, want it unchanged", main, subs, calls)
	}
}

func TestGenerateIsDeterministic(t *testing.T) {
	cfgA, progA, err := Generate(5, 11, 0, "gen_test", "Generated test")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	cfgB, progB, err := Generate(5, 11, 0, "gen_test", "Generated test")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !reflect.DeepEqual(cfgA, cfgB) {
		t.Fatalf("same seed gave different maps:\n%v\n%v", cfgA.Layout, cfgB.Layout)
	}
	if !reflect.DeepEqual(progA, progB) {
		t.Fatalf("same seed gave different programs")
	}
}

func TestGenerateRejectsOutOfRangeSize(t *testing.T) {
	for _, size := range []int{2, 3} {
		if _, _, err := Generate(size, 1, 0, "gen_test", "Generated test"); err == nil {
			t.Fatalf("size %d was accepted, want an error", size)
		}
	}
}

func TestGenerateSmallestBoard(t *testing.T) {
	cfg, prog, err := Generate(4, 1, 0, "gen_test", "Generated test")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	m, err := engine.NewMap(cfg)
	if err != nil {
		t.Fatalf("NewMap: %v", err)
	}
	if err := validate.CheckSolution(m, prog); err != nil {
		t.Fatalf("reference solution: %v", err)
	}
}
