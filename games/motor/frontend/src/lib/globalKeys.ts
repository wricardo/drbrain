/** Page-level shortcuts (outside the tape editor). */
export type GlobalKeyAction = 'back' | 'reset' | 'shortcuts' | 'step';

/** Modifier shortcuts that work anywhere on the page, including from a focused slot. */
export type EditKeyAction = 'undo' | 'redo' | 'run';

type KeyInfo = Pick<KeyboardEvent, 'key' | 'ctrlKey' | 'metaKey' | 'altKey' | 'target'>;

const FIELD = 'input, textarea, select, [contenteditable="true"]';

function inside(e: KeyInfo, selector: string): boolean {
	const el = e.target as HTMLElement | null;
	return !!el && typeof el.closest === 'function' && !!el.closest(selector);
}

/**
 * Maps a window keydown to a page shortcut: R resets, S steps, B steps back, ? opens the
 * shortcuts dialog. Ignored with modifiers, while typing in a form field, and
 * while a program slot or the slot menu has focus (there R means "turn right").
 */
export function globalKeyAction(e: KeyInfo): GlobalKeyAction | null {
	if (e.ctrlKey || e.metaKey || e.altKey) return null;
	if (inside(e, `${FIELD}, [data-slot], [data-testid="slot-picker"]`)) return null;
	if (e.key === '?') return 'shortcuts';
	if (e.key === 'r' || e.key === 'R') return 'reset';
	if (e.key === 's' || e.key === 'S') return 'step';
	if (e.key === 'b' || e.key === 'B') return 'back';
	return null;
}

/**
 * Ctrl/Cmd+Z undoes, Ctrl/Cmd+Shift+Z or Ctrl+Y redoes, Ctrl/Cmd+Enter runs or
 * pauses. Unlike the plain keys these also work from a focused slot (slots never
 * use Ctrl/Cmd), but not in form fields, which keep their native undo.
 */
export function editKeyAction(e: KeyInfo & Pick<KeyboardEvent, 'shiftKey'>): EditKeyAction | null {
	if (e.altKey || !(e.ctrlKey || e.metaKey)) return null;
	if (inside(e, FIELD)) return null;
	const k = e.key.toLowerCase();
	if (k === 'z') return e.shiftKey ? 'redo' : 'undo';
	if (k === 'y' && !e.shiftKey) return 'redo';
	if (e.key === 'Enter') return 'run';
	return null;
}
