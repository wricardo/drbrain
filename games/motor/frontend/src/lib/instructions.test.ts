import { describe, expect, it } from 'vitest';
import type { Instruction } from './types';
import {
	ALL_INSTRUCTIONS,
	CALL_INSTRUCTIONS,
	MAX_SUBS,
	callNumber,
	callTarget,
	instructionForKey,
	instructionMeta,
	isAllowed,
	paletteInstructions,
	subCallKeys
} from './instructions';

describe('call instructions', () => {
	it('knows CALL_SUB_1..MAX_SUBS and nothing beyond', () => {
		expect(MAX_SUBS).toBe(10);
		expect(CALL_INSTRUCTIONS).toHaveLength(MAX_SUBS);
		expect(CALL_INSTRUCTIONS[0]).toBe('CALL_SUB_1');
		expect(CALL_INSTRUCTIONS.at(-1)).toBe('CALL_SUB_10');
		expect(ALL_INSTRUCTIONS).toContain('CALL_SUB_10');
		expect(ALL_INSTRUCTIONS).toHaveLength(4 + MAX_SUBS);
	});

	it('numbers calls 1-based and indexes them 0-based', () => {
		expect(callNumber('CALL_SUB_1')).toBe(1);
		expect(callNumber('CALL_SUB_10')).toBe(10);
		expect(callTarget('CALL_SUB_10')).toBe(9);
		expect(callTarget('MOVE_FORWARD')).toBeNull();
		// Anything the server does not accept as a call must not be treated as one.
		for (const bogus of ['CALL_SUB_11', 'CALL_SUB_01', 'CALL_SUB_0', 'CALL_SUB_', 'CALL_SUB_1 ']) {
			expect(callNumber(bogus as Instruction)).toBeNull();
			expect(callTarget(bogus as Instruction)).toBeNull();
		}
	});

	it('labels and glyphs every call, with sub 10 on key 0', () => {
		expect(instructionMeta('CALL_SUB_4')).toMatchObject({ label: 'Call sub 4', glyph: 'S4', key: '4' });
		expect(instructionMeta('CALL_SUB_10')).toMatchObject({ label: 'Call sub 10', glyph: 'S10', key: '0' });
		expect(instructionMeta('CALL_SUB_10').classes).not.toBe('');
	});

	it('only allows calls the map has subs for', () => {
		expect(isAllowed('CALL_SUB_3', 3)).toBe(true);
		expect(isAllowed('CALL_SUB_4', 3)).toBe(false);
		expect(isAllowed('MOVE_FORWARD', 0)).toBe(true);
	});

	it('offers exactly subCount call tiles in the palette', () => {
		expect(paletteInstructions(0)).toEqual(['MOVE_FORWARD', 'TURN_LEFT', 'TURN_RIGHT']);
		expect(paletteInstructions(10)).toHaveLength(3 + MAX_SUBS);
		expect(paletteInstructions(10).at(-1)).toBe('CALL_SUB_10');
		expect(paletteInstructions(99)).toHaveLength(3 + MAX_SUBS);
	});

	it('binds digits 1-9 and 0 to the calls the map has', () => {
		expect(instructionForKey('4', 4)).toBe('CALL_SUB_4');
		expect(instructionForKey('4', 3)).toBeNull();
		expect(instructionForKey('9', 9)).toBe('CALL_SUB_9');
		expect(instructionForKey('0', 10)).toBe('CALL_SUB_10');
		expect(instructionForKey('0', 9)).toBeNull();
	});

	it('describes the call keys for a help list', () => {
		expect(subCallKeys(0)).toBe('');
		expect(subCallKeys(1)).toBe('1');
		expect(subCallKeys(3)).toBe('1–3');
		expect(subCallKeys(9)).toBe('1–9');
		expect(subCallKeys(10)).toBe('1–9, 0');
	});
});
