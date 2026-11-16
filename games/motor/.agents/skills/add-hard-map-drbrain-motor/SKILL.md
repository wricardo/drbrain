---
name: add-hard-map-drbrain-motor
description: "Design, prove, and ship a new map in drbrain-motor-programming (nesting or recursion): offline BFS/ceiling harness, which geometries are expressible with fixed tapes, max_steps discipline, golden counts, docs/tests touch points, live verification."
---

# Designing and shipping a map in drbrain-motor-programming

Hardness in this game = the declared tape lengths cannot hold the brute-force route. Measure that offline **before** writing any file. Do not eyeball a layout.

## 1. Caps and schema (`game/engine/types.go`, `map-schema.json`)
`MaxSide=32` (rows and cols), `MaxMainTapeLength=32`, `MaxSubTapeLength=16`, `MaxSubs=10`, `MaxMaxSteps=10000`, `max_call_depth<=64`.
Glyphs: `.` `#` `*` and exactly one start `^ > v <`. Optional tiles, off unless a map uses one: conveyors `U D L R`, mirrors `/` `\`, keys `a b c`, doors `A B C`, portals `1`-`9`; `NewMap` rejects a door without its key or a portal digit not appearing twice. Rows 3-32, all rows the same width, id == filename (`^[a-z0-9_-]{1,64}$`), name `<Series> <n> - <Name>` (the card prints the series).

## 2. The forcing bound, and what it can actually prove
- `minActions` = BFS over `(pos, facing, treat bitmask)`; TURN and MOVE cost 1 each, blocked moves can be skipped (same state, never optimal). Bits **must** be `1<<i`; plain indices silently corrupt masks and BFS returns None.
- `maxLeafActions(levels)` = `tape(n,used,d)=n*max over unused subs tape(l,used|bit,d-1)`, `tape(l,_,0)=l`, i.e. `main_len * product of the levels largest distinct sub lengths`. In the test the entry is `{id, minNeeded, levels, ceiling}` and it asserts `ceiling < minNeeded`.
- Proving depth 2 needs `minActions > main_len * top2`. **Know this before you design a shape**: object-shaped maps carry long composite tapes (a 4-arm visit tape is 8 slots, a ray half 6-11), so `top2` lands in the hundreds and only `levels=1` is provable unless the route is very long. Measured this session: star of 8 dead-end rays, main 4, subs `[11,2,6,7,8]` => ceiling(1)=44, ceiling(2)=1760, minActions 209. Claim `levels=1` and say so in the docs row; never inflate it.
- Treat placement decides what `minActions` proves:
  - single corridor: one treat anywhere forces the whole route;
  - cycle (a ring walked once): one treat per tooth forces the full loop;
  - tree of dead ends: treats at a few tips prove far less than the tour (8-12 spread tips measured 97-114 against a 223-action route). Never advertise the solution's action count as "needed".

## 3. Which geometries are expressible (fixed tapes = fixed sizes)
You get `main` + at most 5 subs = 6 tapes; every distinct length or role needs its own tape.
- Expresssible: sweeps, combs, rings with equal teeth/rungs, grids of identical cells, stars of equal rays.
- **Not** expressible: concentric rings, shrinking spirals, crosses of different-sized crosses — each size would need its own call count, hence its own tape. Do not spend a session discovering this again.
- A rotation unit called four times closes on itself: any unit that advances and turns 90 degrees ends up back where it started (`[F,F,F,T]` traces a square). Rotating designs are therefore rings, not outward spirals.
- Turns are relative, so **one tape serves all four orientations** (`R D L U`); only genuine geometry (a corner gap) needs a pinned `TURN_LEFT`.
Recipes that worked:
- Long straight runs: `W = k*a + r` => one sub of `a` moves, one sub that calls it `k` times plus `r` literal moves (31 = 5x6+1).
- Dead-end excursion that returns facing the caller's direction: `[F x L, TL, TL, F x L, TL, TL]`. Dropping the second flip pair makes the unit end facing backwards; the caller's rotation bookkeeping then breaks and the robot re-walks one arm while missing another (it still reports 0 bumps — check coverage, not just bumps).
- Visit 3 of 4 arms, skipping the entry (7 slots): `[V, TR, V, TR, TR, V, TR]`. The mirrored entry order needs the `TURN_LEFT` twin.
- Ring walked once from 4 main slots: `main = [side]x4`, `side = [TR, ray, TL, straightGap, TR, ray, TL, cornerGap]` — the `TR`/`TL` pair turns into a ray and back, the gaps carry the rim.
- Equal gaps: put teeth/rays at the quarter points of a rim side. Rim side 21, quarter 5 => straight gap 10 and corner gap 5+turn+5, so one gap tape per kind.
- Grid arithmetic: a 32x32 grid with 1-cell corridors and 1-cell walls gives 16 lanes; a rim of cells [lo..hi]^2 plus outward rays of L reaches the board edge when `L = lo`.

## 4. Calibrate the replica before trusting a single number
Reproduce, exactly, before designing:
- every golden step count in `validate/validate_test.go` (`TestEveryMapSolutionWinsWithGoldenSteps`);
- the documented `minActions` values in `docs/DEVELOPMENT.md` (serpentine 61, powers 19, pinwheel 35, bump_spiral 56, comb 51, ladder 42, diamond_ring 37, switchback 36, mountain_range 12).
Tapes are **exact length**: the player's program must fill `main_tape_length` slots and each `sub_tape_lengths[i]` exactly, so an `EMPTY` slot costs length but no step. Frame depth = `len(callStack)-1`. Step semantics: CALL costs one step; EMPTY is skipped and does not count; a move into rock or off-grid does nothing but still costs a step (wall bump); the win check fires after each step, so a win beats the step limit on the same step; `steps >= max_steps` loses; with recursion off a call to a sub already on the stack is a no-op that still spends a step. Call mapping is `CALL_SUB_n -> tapes[n]` with `tapes=[main]+subs` (off-by-one here produces plausible nonsense).

## 5. Choose `max_steps` with measurements, not intuition
- Simulate the **two natural decompositions**: literal-move gaps vs a `run` sub plus a wide main. The slower legal one sets the floor; add ~15.
- Brute-force the content variants of the tightest tape (e.g. every arrangement of 9 moves + 2 turns in 11 slots). If only the intended one wins, the geometry is doing the work and the budget is not the puzzle.
- Also check coverage: count visited cells and confirm every treat is reached and no corridor cell is left unused by design.

## 6. Wire it in (checklist)
1. `maps/<id>.json`; if you replace a design, delete the old map **and** its local solution, golden, bound case and docs rows (`len(files) != len(golden)` catches half-removals).
2. `solutions/<id>.json` — exact tape lengths. Local only: never commit, never put it in the schema, prompts, `llms.txt`, `static/`, or a bd issue (bd syncs to the public remote).
3. `just validate` -> every map `OK ... WON in N steps`.
4. `validate/validate_test.go`: golden count + `{id, minNeeded, levels, ceiling}` case.
5. `game/config/manager_test.go` `TestRepoMapsLoad` shipped count; `README.md` count; `docs/DEVELOPMENT.md` (shipped count, series bullet, forcing-table row, the "Only the N bounds asserted in ..." sentence, golden list in the Engine change checklist).
6. `gofmt -w` the test file (the aligned struct-literal comments are gofmt-sensitive), then `just validate`, `go test ./...`, `just verify`, `just smoke 9191`.

## 7. Prove it end to end
`go run . -port <free> -host 127.0.0.1 -debug`, then GraphQL: `createSession(mapID)` (keep `ownerToken`) -> `setProgram(sessionID, ownerToken, program)` -> `run(speedMs: 50)` -> poll `session(id){ vm{ status steps treatsRemaining } }`.
UI: `/watch/<sessionID>`; a `/play/<mapID>` URL silently falls back to another map, so use the session id. `VMState` has `treatsRemaining`, not `treatsLeft`. Board: a 32x32 map renders ~20px cells at a 1400px viewport; screenshot the board `svg` and look at the shape, because a legal map can still read as a dark slab.

## 8. This repo is edited concurrently
Another agent may land maps, engine tiles, schema and docs in the same working tree. Re-read a file before editing it, re-count `ls maps/*.json` before writing a count, keep your diff to your own map's lines, and never revert hunks you did not write. Route: solve/ship at `/Users/wricardo/go/src/github.com/wricardo/drbrain-motor-programming`, gates `just validate` + `just test`.
