import { describe, expect, it } from 'vitest';
import { buildRunRows, countRows, filterRows, runText, runsHref, shareUrl, solvedSummary } from './runs';
import type { GameMap, PlayerRun } from './types';

const map = (id: string, name: string, difficulty: string) => ({ id, name, difficulty }) as GameMap;
const maps = [map('h', 'Patterns 1', 'hard'), map('e2', 'Level 2', 'easy'), map('e1', 'Level 1', 'easy'), map('m', 'Recursion 1', 'medium')];

const run = (mapId: string, slots = 5, steps = 8, rank = 2, total = 9): PlayerRun => ({
	mapId,
	total,
	entry: { rank, sessionId: 'aaaaaaaaaaaaaaaa', displayName: 'BraveOtter', handle: null, mine: false, slots, steps, achievedAt: '2026-01-02T03:04:05Z' }
});

describe('buildRunRows', () => {
	const rows = buildRunRows(maps, [run('e1'), run('h'), run('gone')]);
	it('lists every map once, easy to hard, with its run or null', () => {
		expect(rows.map((r) => r.map.id)).toEqual(['e1', 'e2', 'm', 'h']);
		expect(rows.map((r) => r.run?.mapId ?? null)).toEqual(['e1', null, null, 'h']);
	});
	it('drops runs for maps that no longer exist', () => {
		expect(rows.some((r) => r.run?.mapId === 'gone')).toBe(false);
	});
	it('works with no runs and no maps', () => {
		expect(buildRunRows(maps, []).every((r) => r.run === null)).toBe(true);
		expect(buildRunRows([], [run('e1')])).toEqual([]);
	});
});

describe('filters and counts', () => {
	const rows = buildRunRows(maps, [run('e1'), run('h')]);
	it('filters', () => {
		expect(filterRows(rows, 'all')).toHaveLength(4);
		expect(filterRows(rows, 'solved').map((r) => r.map.id)).toEqual(['e1', 'h']);
		expect(filterRows(rows, 'unsolved').map((r) => r.map.id)).toEqual(['e2', 'm']);
	});
	it('counts per chip', () => {
		expect(countRows(rows)).toEqual({ all: 4, solved: 2, unsolved: 2 });
	});
	it('summarises', () => {
		expect(solvedSummary(rows)).toBe('Solved 2 of 4 maps');
		expect(solvedSummary(buildRunRows([maps[0]], []))).toBe('Solved 0 of 1 map');
	});
	it('counts the official campaign on its own once community maps exist, and lists them last', () => {
		const community = { id: 'zig', name: 'Claude Zigzag 7x5', difficulty: 'easy', builtin: false } as GameMap;
		const official = maps.map((m) => ({ ...m, builtin: true }) as GameMap);
		const withCommunity = buildRunRows([community, ...official], [run('e1'), run('zig')]);
		expect(withCommunity.map((r) => r.map.id)).toEqual(['e1', 'e2', 'm', 'h', 'zig']);
		expect(solvedSummary(withCommunity)).toBe('Solved 1 of 4 official maps · 1 of 1 community map');
	});
});

describe('links and text', () => {
	it('builds a runs link from a name', () => {
		expect(runsHref('BraveOtter')).toBe('/runs?name=BraveOtter');
		expect(runsHref('  Ada Lovelace & co ')).toBe('/runs?name=Ada%20Lovelace%20%26%20co');
		expect(runsHref('   ')).toBeNull();
	});
	it('builds a shareable absolute URL', () => {
		expect(shareUrl('https://x.test', 'BraveOtter')).toBe('https://x.test/runs?name=BraveOtter');
		expect(shareUrl('https://x.test', '')).toBeNull();
	});
	it('describes a run', () => {
		expect(runText(run('e1', 5, 8))).toBe('5 slots · 8 steps');
		expect(runText(run('e1', 1, 1))).toBe('1 slot · 1 step');
	});
});
