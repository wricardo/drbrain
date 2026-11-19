import { cloneProgram } from './program';
import type { Program } from './types';

/** Undo/redo stacks for program edits. Programs are replaced, never mutated, so snapshots are cheap. */
export interface EditHistory {
	past: Program[];
	future: Program[];
}

/** How many edits can be undone. */
export const HISTORY_LIMIT = 100;

export const emptyHistory: EditHistory = { past: [], future: [] };

export const canUndo = (h: EditHistory): boolean => h.past.length > 0;
export const canRedo = (h: EditHistory): boolean => h.future.length > 0;

/** Records that `current` is about to be replaced by a new edit: it becomes undoable and redo is dropped. */
export function recordEdit(h: EditHistory, current: Program): EditHistory {
	const past = [...h.past, cloneProgram(current)];
	return { past: past.slice(-HISTORY_LIMIT), future: [] };
}

/** Steps back one edit; null when there is nothing to undo. `current` is kept for redo. */
export function undo(h: EditHistory, current: Program): { history: EditHistory; program: Program } | null {
	const program = h.past.at(-1);
	if (!program) return null;
	return { history: { past: h.past.slice(0, -1), future: [...h.future, cloneProgram(current)] }, program };
}

/** Steps forward one undone edit; null when there is nothing to redo. */
export function redo(h: EditHistory, current: Program): { history: EditHistory; program: Program } | null {
	const program = h.future.at(-1);
	if (!program) return null;
	return { history: { past: [...h.past, cloneProgram(current)], future: h.future.slice(0, -1) }, program };
}
