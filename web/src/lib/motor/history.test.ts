import { describe, expect, it } from 'vitest';
import { HISTORY_LIMIT, canRedo, canUndo, emptyHistory, recordEdit, redo, undo } from './history';
import { emptyProgram, setSlot } from './program';

const cfg = { mainTapeLength: 3, subTapeLengths: [2] };
const p0 = emptyProgram(cfg);
const p1 = setSlot(p0, -1, 0, 'MOVE_FORWARD');
const p2 = setSlot(p1, -1, 1, 'TURN_LEFT');

describe('edit history', () => {
	it('starts empty: nothing to undo or redo', () => {
		expect(canUndo(emptyHistory)).toBe(false);
		expect(canRedo(emptyHistory)).toBe(false);
		expect(undo(emptyHistory, p0)).toBeNull();
		expect(redo(emptyHistory, p0)).toBeNull();
	});

	it('undo restores the previous program and redo brings the edit back', () => {
		let h = recordEdit(emptyHistory, p0); // p0 -> p1
		h = recordEdit(h, p1); // p1 -> p2
		const back = undo(h, p2)!;
		expect(back.program).toEqual(p1);
		const back2 = undo(back.history, back.program)!;
		expect(back2.program).toEqual(p0);
		expect(canUndo(back2.history)).toBe(false);

		const fwd = redo(back2.history, back2.program)!;
		expect(fwd.program).toEqual(p1);
		const fwd2 = redo(fwd.history, fwd.program)!;
		expect(fwd2.program).toEqual(p2);
		expect(redo(fwd2.history, fwd2.program)).toBeNull();
	});

	it('a new edit after an undo discards the redo branch', () => {
		const h = recordEdit(emptyHistory, p0);
		const back = undo(h, p1)!;
		expect(canRedo(back.history)).toBe(true);
		const edited = recordEdit(back.history, back.program);
		expect(canRedo(edited)).toBe(false);
	});

	it('stores copies, so later changes to a program do not rewrite history', () => {
		const live = setSlot(p0, -1, 0, 'MOVE_FORWARD');
		const h = recordEdit(emptyHistory, live);
		live.main[0] = 'TURN_RIGHT';
		expect(h.past[0].main[0]).toBe('MOVE_FORWARD');
	});

	it('keeps only the most recent HISTORY_LIMIT edits', () => {
		let h = emptyHistory;
		for (let i = 0; i < HISTORY_LIMIT + 10; i++) h = recordEdit(h, i % 2 ? p1 : p2);
		expect(h.past).toHaveLength(HISTORY_LIMIT);
	});
});
