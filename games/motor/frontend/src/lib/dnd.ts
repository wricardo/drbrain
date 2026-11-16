import { writable } from 'svelte/store';
import type { Instruction } from './types';
import type { DragSource, DropMode, SlotRef } from './program';

/** Custom MIME type carrying a DragSource as JSON. */
export const DRAG_MIME = 'application/x-drbrain-slot';

/** Serializes a drag source for dataTransfer. */
export function encodeDrag(source: DragSource): string {
	return JSON.stringify(source);
}

const INSTRUCTIONS: readonly string[] = [
	'EMPTY',
	'TURN_LEFT',
	'TURN_RIGHT',
	'MOVE_FORWARD',
	'CALL_SUB_1',
	'CALL_SUB_2',
	'CALL_SUB_3'
];

/** Parses dataTransfer text back into a DragSource; returns null for anything malformed. */
export function decodeDrag(raw: string): DragSource | null {
	try {
		const v = JSON.parse(raw) as Record<string, unknown>;
		if (v.kind === 'palette' && typeof v.instruction === 'string' && INSTRUCTIONS.includes(v.instruction)) {
			return { kind: 'palette', instruction: v.instruction as Instruction };
		}
		if (v.kind === 'slot' && Number.isInteger(v.tape) && Number.isInteger(v.slot)) {
			return { kind: 'slot', tape: v.tape as number, slot: v.slot as number };
		}
	} catch {
		/* not ours */
	}
	return null;
}

/** The drag in progress, or null. dragover can't read dataTransfer, so components share it here. */
export const activeDrag = writable<DragSource | null>(null);

/** Modifier keys held during a drag. */
export interface DragModifiers {
	altKey: boolean;
	ctrlKey: boolean;
	metaKey: boolean;
	shiftKey: boolean;
}

/**
 * What dropping `source` on a slot does: Alt/Ctrl/Cmd copy, Shift inserts (reorders
 * within a tape), otherwise a swap. Palette drops always place a copy of the instruction.
 */
export function dropModeFor(source: DragSource, mods: DragModifiers): DropMode {
	if (source.kind === 'palette') return 'copy';
	if (mods.altKey || mods.ctrlKey || mods.metaKey) return 'copy';
	return mods.shiftKey ? 'insert' : 'move';
}

/** The dataTransfer.dropEffect that matches a mode (drives the cursor). */
export function dropEffectFor(mode: DropMode): 'copy' | 'move' {
	return mode === 'copy' ? 'copy' : 'move';
}

/**
 * The slot an erase-drop would clear, or null. Only a placed step can be erased: dragging a
 * palette tile (or anything else) onto the palette is a no-op, and a locked palette accepts nothing.
 */
export function eraseTargetFor(source: DragSource | null, disabled = false): SlotRef | null {
	if (disabled || source?.kind !== 'slot') return null;
	return { tape: source.tape, slot: source.slot };
}
