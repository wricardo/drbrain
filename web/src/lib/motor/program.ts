import { callTarget } from './instructions';
import type { Instruction, Program } from './types';

/** Tape lengths of a map (the subset of GameMap this module needs). */
export interface TapeConfig {
	mainTapeLength: number;
	subTapeLengths: number[];
}

/** Where a drag started: the palette, or an existing slot. */
export type DragSource =
	| { kind: 'palette'; instruction: Instruction }
	| { kind: 'slot'; tape: number; slot: number };

export interface SlotRef {
	tape: number;
	slot: number;
}

/** Builds an all-EMPTY program shaped for the map's tape config. */
export function emptyProgram(cfg: TapeConfig): Program {
	return {
		main: Array<Instruction>(cfg.mainTapeLength).fill('EMPTY'),
		subs: cfg.subTapeLengths.map((n) => Array<Instruction>(n).fill('EMPTY'))
	};
}

/** Returns a new program with every slot on every tape EMPTY, keeping the tape lengths. */
export function clearProgram(p: Program): Program {
	return {
		main: Array<Instruction>(p.main.length).fill('EMPTY'),
		subs: p.subs.map((s) => Array<Instruction>(s.length).fill('EMPTY'))
	};
}

/** True when at least one slot on any tape holds an instruction. */
export function hasInstructions(p: Program): boolean {
	return p.main.some((i) => i !== 'EMPTY') || p.subs.some((s) => s.some((i) => i !== 'EMPTY'));
}

/** How many slots of a tape hold an instruction. */
export function usedSlots(slots: readonly Instruction[]): number {
	return slots.filter((i) => i !== 'EMPTY').length;
}

/**
 * Subs (0-based) that hold instructions but can never run because nothing reachable
 * from main calls them. Empty subs are not reported: there is nothing to run.
 */
export function unreachableSubs(p: Program): number[] {
	const reached = new Set<number>();
	const queue: Instruction[][] = [p.main];
	while (queue.length) {
		for (const ins of queue.pop()!) {
			const sub = callTarget(ins);
			if (sub === null || reached.has(sub) || sub >= p.subs.length) continue;
			reached.add(sub);
			queue.push(p.subs[sub]);
		}
	}
	return p.subs.map((s, i) => (!reached.has(i) && usedSlots(s) > 0 ? i : -1)).filter((i) => i >= 0);
}

/** Deep-copies a program (also strips any reactive proxy wrappers). */
export function cloneProgram(p: Program): Program {
	return { main: [...p.main], subs: p.subs.map((s) => [...s]) };
}

/** Structural equality of two programs. */
export function programsEqual(a: Program, b: Program): boolean {
	if (a.main.length !== b.main.length || a.subs.length !== b.subs.length) return false;
	if (a.main.some((v, i) => v !== b.main[i])) return false;
	return a.subs.every((s, i) => s.length === b.subs[i].length && s.every((v, j) => v === b.subs[i][j]));
}

/** Returns the tape (-1 main, 0.. subs) or undefined when out of range. */
export function tapeOf(p: Program, tape: number): Instruction[] | undefined {
	return tape < 0 ? (tape === -1 ? p.main : undefined) : p.subs[tape];
}

/** Tape lengths ordered [main, sub1, sub2, ...]. */
export function tapeLengths(p: Program): number[] {
	return [p.main.length, ...p.subs.map((s) => s.length)];
}

/** Returns a new program with one slot replaced; out-of-range targets return the input unchanged. */
export function setSlot(p: Program, tape: number, slot: number, ins: Instruction): Program {
	const t = tapeOf(p, tape);
	if (!t || slot < 0 || slot >= t.length) return p;
	const next = cloneProgram(p);
	const nt = tapeOf(next, tape)!;
	nt[slot] = ins;
	return next;
}

/**
 * What a slot drop does: `move` swaps with the target (nothing is lost), `copy`
 * duplicates, `insert` reorders within one tape, shifting the slots in between.
 */
export type DropMode = 'move' | 'copy' | 'insert';

/**
 * Reorders one tape: lifts the slot at `from` and re-inserts it at `to`, shifting
 * the slots in between by one. Cross-tape or out-of-range requests return the input.
 */
export function insertSlot(p: Program, from: SlotRef, to: SlotRef): Program {
	if (from.tape !== to.tape) return p;
	const t = tapeOf(p, from.tape);
	if (!t || from.slot === to.slot) return p;
	if (from.slot < 0 || from.slot >= t.length || to.slot < 0 || to.slot >= t.length) return p;
	const next = cloneProgram(p);
	const nt = tapeOf(next, from.tape)!;
	const [moved] = nt.splice(from.slot, 1);
	nt.splice(to.slot, 0, moved);
	return next;
}

/**
 * Applies a drag-and-drop. Palette drops place the instruction. Slot drops follow
 * `mode`; `insert` across different tapes falls back to a swap.
 */
export function applyDrop(p: Program, source: DragSource, target: SlotRef, mode: DropMode): Program {
	if (source.kind === 'palette') return setSlot(p, target.tape, target.slot, source.instruction);
	const from = tapeOf(p, source.tape)?.[source.slot];
	const to = tapeOf(p, target.tape)?.[target.slot];
	if (from === undefined || to === undefined) return p;
	if (source.tape === target.tape && source.slot === target.slot) return p;
	if (mode === 'insert' && source.tape === target.tape) return insertSlot(p, source, target);
	let next = setSlot(p, target.tape, target.slot, from);
	if (mode !== 'copy') next = setSlot(next, source.tape, source.slot, to);
	return next;
}
