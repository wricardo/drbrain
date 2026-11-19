import { describe, expect, it } from 'vitest';
import {
	boardHref,
	isMine,
	orderOption,
	orderParam,
	parseOrder,
	pickMap,
	rankBadgeClass,
	rankLabel,
	rankSummary,
	slotsText,
	stepsText
} from './leaderboard';
import type { LeaderboardEntry } from './types';

const entry = (over: Partial<LeaderboardEntry> = {}): LeaderboardEntry => ({
	rank: 1,
	sessionId: 'aaaaaaaaaaaaaaaa',
	displayName: 'Ada',
	handle: null,
	mine: false,
	slots: 6,
	steps: 9,
	achievedAt: '2026-01-02T03:04:05Z',
	...over
});

describe('order params', () => {
	it('maps URL values to orders and defaults to slots', () => {
		expect(parseOrder('steps')).toBe('STEPS');
		expect(parseOrder('STEPS')).toBe('STEPS');
		expect(parseOrder('slots')).toBe('SLOTS');
		expect(parseOrder('nonsense')).toBe('SLOTS');
		expect(parseOrder(null)).toBe('SLOTS');
		expect(parseOrder(undefined)).toBe('SLOTS');
	});
	it('round-trips', () => {
		for (const o of ['SLOTS', 'STEPS'] as const) expect(parseOrder(orderParam(o))).toBe(o);
	});
	it('describes each board', () => {
		expect(orderOption('SLOTS').rule).toMatch(/fewest slots used, then fewest steps/);
		expect(orderOption('STEPS').rule).toMatch(/fewest steps, then fewest slots/);
	});
});

describe('boardHref', () => {
	it('leaves out the default order', () => {
		expect(boardHref('serpentine')).toBe('/motor/leaderboard?map=serpentine');
		expect(boardHref('serpentine', 'SLOTS')).toBe('/motor/leaderboard?map=serpentine');
		expect(boardHref('serpentine', 'STEPS')).toBe('/motor/leaderboard?map=serpentine&order=steps');
	});
	it('encodes the map id', () => {
		expect(boardHref('a b&c')).toBe('/motor/leaderboard?map=a+b%26c');
	});
});

describe('pickMap', () => {
	const maps = [
		{ id: 'hard1', name: 'Level 9', difficulty: 'hard' },
		{ id: 'easy2', name: 'Level 2', difficulty: 'easy' },
		{ id: 'easy1', name: 'Level 1', difficulty: 'easy' }
	];
	it('keeps the requested map when it exists', () => {
		expect(pickMap(maps, 'hard1')?.id).toBe('hard1');
	});
	it('falls back to the first map in the standard order', () => {
		expect(pickMap(maps, 'gone')?.id).toBe('easy1');
		expect(pickMap(maps, null)?.id).toBe('easy1');
	});
	it('is null without maps', () => {
		expect(pickMap([], 'x')).toBeNull();
	});
});

describe('isMine', () => {
	it('matches on the session id', () => {
		expect(isMine(entry(), ['bbbbbbbbbbbbbbbb', 'aaaaaaaaaaaaaaaa'])).toBe(true);
		expect(isMine(entry(), new Set(['bbbbbbbbbbbbbbbb']))).toBe(false);
		expect(isMine(entry(), [])).toBe(false);
	});
});

describe('text helpers', () => {
	it('pluralises', () => {
		expect(slotsText(1)).toBe('1 slot');
		expect(slotsText(6)).toBe('6 slots');
		expect(stepsText(1)).toBe('1 step');
		expect(stepsText(9)).toBe('9 steps');
	});
	it('words ranks', () => {
		expect([1, 2, 3, 4, 11, 12, 13, 21, 22, 101, 111].map(rankLabel)).toEqual([
			'1st place',
			'2nd place',
			'3rd place',
			'4th place',
			'11th place',
			'12th place',
			'13th place',
			'21st place',
			'22nd place',
			'101st place',
			'111th place'
		]);
	});
	it('gives the podium its own colours', () => {
		const classes = [1, 2, 3, 4].map(rankBadgeClass);
		expect(new Set(classes).size).toBe(4);
		expect(rankBadgeClass(4)).toBe(rankBadgeClass(40));
	});
});

describe('rankSummary', () => {
	it('summarises an entry', () => {
		expect(rankSummary(entry({ rank: 2 }), 14)).toBe('Rank 2 of 14 · 6 slots · 9 steps');
		expect(rankSummary(entry({ rank: 1, slots: 1, steps: 1 }), 1)).toBe('Rank 1 of 1 · 1 slot · 1 step');
	});
	it('is null without an entry', () => {
		expect(rankSummary(null, 14)).toBeNull();
	});
});
