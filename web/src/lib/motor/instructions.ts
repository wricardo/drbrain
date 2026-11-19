import type { Instruction } from './types';

export interface InstructionMeta {
	label: string;
	/** Short text glyph shown on slots and palette buttons. */
	glyph: string;
	/** Keyboard shortcut (case-insensitive) that places this instruction into the selected slot. */
	key: string;
	/** Tailwind classes for a filled slot / palette button. */
	classes: string;
}

/** Highest subroutine index the UI can call (mirrors the server's MaxSubs). */
export const MAX_SUBS = 10;

/**
 * Cycled fill/border palette for call tiles, one tone per sub. The engine allows up to
 * MAX_SUBS subs and dark mode only remaps the colour families used here, so the ten
 * entries reuse nine families (sub 10 repeats violet; the S-glyph still distinguishes them).
 */
const CALL_TONES: { classes: string; key: string }[] = [
	{ classes: 'bg-violet-100 text-violet-900 border-violet-400', key: '1' },
	{ classes: 'bg-fuchsia-100 text-fuchsia-900 border-fuchsia-400', key: '2' },
	{ classes: 'bg-rose-100 text-rose-900 border-rose-400', key: '3' },
	{ classes: 'bg-orange-100 text-orange-900 border-orange-400', key: '4' },
	{ classes: 'bg-amber-100 text-amber-900 border-amber-400', key: '5' },
	{ classes: 'bg-emerald-100 text-emerald-900 border-emerald-400', key: '6' },
	{ classes: 'bg-sky-100 text-sky-900 border-sky-400', key: '7' },
	{ classes: 'bg-indigo-100 text-indigo-900 border-indigo-400', key: '8' },
	{ classes: 'bg-slate-100 text-slate-900 border-slate-400', key: '9' },
	{ classes: 'bg-violet-100 text-violet-900 border-violet-400', key: '0' }
];

const BASE_META: Record<'EMPTY' | 'TURN_LEFT' | 'TURN_RIGHT' | 'MOVE_FORWARD', InstructionMeta> = {
	EMPTY: { label: 'Empty', glyph: '', key: '', classes: 'bg-white text-slate-400 border-slate-300' },
	TURN_LEFT: { label: 'Turn left', glyph: '↺', key: 'L', classes: 'bg-sky-100 text-sky-900 border-sky-400' },
	TURN_RIGHT: { label: 'Turn right', glyph: '↻', key: 'R', classes: 'bg-amber-100 text-amber-900 border-amber-400' },
	MOVE_FORWARD: { label: 'Move forward', glyph: '↑', key: 'F', classes: 'bg-emerald-100 text-emerald-900 border-emerald-400' }
};

/** Display metadata for a CALL_SUB_k instruction (k = 1..MAX_SUBS). */
function callMeta(k: number): InstructionMeta {
	const tone = CALL_TONES[(k - 1) % CALL_TONES.length];
	return { label: `Call sub ${k}`, glyph: `S${k}`, key: tone.key, classes: tone.classes };
}

/** Returns display metadata for an instruction, including every CALL_SUB_k. */
export function instructionMeta(ins: Instruction): InstructionMeta {
	if (ins === 'EMPTY' || ins === 'TURN_LEFT' || ins === 'TURN_RIGHT' || ins === 'MOVE_FORWARD') {
		return BASE_META[ins];
	}
	const k = callNumber(ins);
	return k === null ? { label: ins, glyph: '', key: '', classes: '' } : callMeta(k);
}

/** Returns the 1-based sub number a CALL_SUB_k instruction calls, or null for anything else. */
export function callNumber(ins: Instruction): number | null {
	const m = /^CALL_SUB_([1-9]\d?)$/.exec(ins);
	if (!m) return null;
	const k = Number(m[1]);
	return k <= MAX_SUBS ? k : null;
}

/** Returns the 0-based sub index an instruction calls, or null for non-call instructions. */
export function callTarget(ins: Instruction): number | null {
	const k = callNumber(ins);
	return k === null ? null : k - 1;
}

/** True when the instruction may appear in a program with subCount subroutines. */
export function isAllowed(ins: Instruction, subCount: number): boolean {
	const target = callTarget(ins);
	return target === null || target < subCount;
}

/** Every call instruction the UI knows about, in sub order. */
export const CALL_INSTRUCTIONS: Instruction[] = Array.from(
	{ length: MAX_SUBS },
	(_, i) => `CALL_SUB_${i + 1}` as Instruction
);

/** Instructions offered by the palette (excluding EMPTY) for a map with subCount subs. */
export function paletteInstructions(subCount: number): Instruction[] {
	const base: Instruction[] = ['MOVE_FORWARD', 'TURN_LEFT', 'TURN_RIGHT'];
	return [...base, ...CALL_INSTRUCTIONS.slice(0, Math.max(0, Math.min(subCount, MAX_SUBS)))];
}

/** All instructions, including EMPTY: the complete set the UI can render or decode. */
export const ALL_INSTRUCTIONS: Instruction[] = ['EMPTY', 'TURN_LEFT', 'TURN_RIGHT', 'MOVE_FORWARD', ...CALL_INSTRUCTIONS];

/** Finds the instruction bound to a keyboard key, restricted to those allowed for subCount. */
export function instructionForKey(key: string, subCount: number): Instruction | null {
	const k = key.toUpperCase();
	for (const ins of ALL_INSTRUCTIONS) {
		const meta = instructionMeta(ins);
		if (meta.key === k && isAllowed(ins, subCount)) return ins;
	}
	return null;
}

/** Human label for a tape index (-1 = main). */
export function tapeLabel(tape: number): string {
	return tape < 0 ? 'Main' : `Sub ${tape + 1}`;
}

/**
 * The keyboard keys that call subs 1..subCount, written for a help list: subs 1-9 use
 * their own digit and sub 10 uses 0, so ten subs read "1–9, 0".
 */
export function subCallKeys(subCount: number): string {
	const n = Math.max(0, Math.min(subCount, MAX_SUBS));
	if (n === 0) return '';
	if (n === 1) return '1';
	if (n <= 9) return `1–${n}`;
	return '1–9, 0';
}
