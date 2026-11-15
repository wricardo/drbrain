import { describe, expect, it } from 'vitest';
import { decodeDrag, dropEffectFor, dropModeFor, encodeDrag } from './dnd';

describe('drag payload', () => {
	it('round-trips palette and slot sources', () => {
		const a = { kind: 'palette', instruction: 'CALL_SUB_2' } as const;
		const b = { kind: 'slot', tape: -1, slot: 3 } as const;
		expect(decodeDrag(encodeDrag(a))).toEqual(a);
		expect(decodeDrag(encodeDrag(b))).toEqual(b);
	});

	it('rejects foreign or malformed payloads', () => {
		expect(decodeDrag('hello')).toBeNull();
		expect(decodeDrag('{"kind":"palette","instruction":"DROP_TABLE"}')).toBeNull();
		expect(decodeDrag('{"kind":"slot","tape":"a","slot":1}')).toBeNull();
	});

	describe('drop mode', () => {
		const none = { altKey: false, ctrlKey: false, metaKey: false, shiftKey: false };
		const slot = { kind: 'slot', tape: -1, slot: 0 } as const;
		const palette = { kind: 'palette', instruction: 'MOVE_FORWARD' } as const;

		it('swaps by default, copies with Alt/Ctrl/Cmd, inserts with Shift', () => {
			expect(dropModeFor(slot, none)).toBe('move');
			expect(dropModeFor(slot, { ...none, altKey: true })).toBe('copy');
			expect(dropModeFor(slot, { ...none, ctrlKey: true })).toBe('copy');
			expect(dropModeFor(slot, { ...none, metaKey: true })).toBe('copy');
			expect(dropModeFor(slot, { ...none, shiftKey: true })).toBe('insert');
		});

		it('copy beats insert when both are held', () => {
			expect(dropModeFor(slot, { ...none, altKey: true, shiftKey: true })).toBe('copy');
		});

		it('palette drops are always copies, whatever is held', () => {
			expect(dropModeFor(palette, none)).toBe('copy');
			expect(dropModeFor(palette, { ...none, shiftKey: true })).toBe('copy');
		});

		it('maps modes to a cursor effect', () => {
			expect(dropEffectFor('copy')).toBe('copy');
			expect(dropEffectFor('move')).toBe('move');
			expect(dropEffectFor('insert')).toBe('move');
		});
	});
});
