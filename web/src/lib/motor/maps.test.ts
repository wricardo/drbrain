import { describe, expect, it } from 'vitest';
import { canEditMap, countByDifficulty, filterMaps, groupMaps, isDraft, isOfficial, nextMap, sortMaps, splitMapName } from './maps';

describe('sortMaps', () => {
	it('orders easy -> medium -> hard, then by name, unknown last, without mutating', () => {
		const input = [
			{ name: 'B', difficulty: 'hard' },
			{ name: 'Z', difficulty: 'weird' },
			{ name: 'C', difficulty: 'easy' },
			{ name: 'A', difficulty: 'hard' },
			{ name: 'M', difficulty: 'medium' },
			{ name: 'A2', difficulty: 'easy' }
		];
		const copy = structuredClone(input);
		expect(sortMaps(input).map((m) => m.name)).toEqual(['A2', 'C', 'M', 'A', 'B', 'Z']);
		expect(input).toEqual(copy);
	});

	it('keeps series order by number within a difficulty', () => {
		const input = [
			{ name: 'Patterns 10 - X', difficulty: 'hard' },
			{ name: 'Patterns 4 - Comb', difficulty: 'hard' },
			{ name: 'Patterns 1 - Serpentine', difficulty: 'hard' }
		];
		expect(sortMaps(input).map((m) => m.name)).toEqual(['Patterns 1 - Serpentine', 'Patterns 4 - Comb', 'Patterns 10 - X']);
	});
});

describe('official vs community maps', () => {
	const official = (id: string, name: string, difficulty: string) => ({ id, name, difficulty, builtin: true });
	const community = (id: string, name: string, difficulty: string) => ({ id, name, difficulty, builtin: false });
	const mixed = [
		community('zig', 'Claude Zigzag 7x5', 'easy'),
		official('l5', 'Level 5 - Zigzag Path', 'medium'),
		official('l1', 'Level 1 - Straight Line', 'easy'),
		community('c2', 'A community hard one', 'hard'),
		official('p1', 'Patterns 1 - Serpentine', 'hard')
	];

	it('isOfficial treats a missing flag as official and only builtin: false as community', () => {
		expect(isOfficial({})).toBe(true);
		expect(isOfficial({ builtin: true })).toBe(true);
		expect(isOfficial({ builtin: false })).toBe(false);
	});

	it('sortMaps lists every official map before any community map, easy to hard inside each', () => {
		expect(sortMaps(mixed).map((m) => m.id)).toEqual(['l1', 'l5', 'p1', 'zig', 'c2']);
	});

	it('groupMaps returns the two groups in order, each sorted, and drops an empty group', () => {
		const groups = groupMaps(mixed);
		expect(groups.map((g) => [g.id, g.label, g.maps.map((m) => m.id)])).toEqual([
			['official', 'Official maps', ['l1', 'l5', 'p1']],
			['community', 'Community maps', ['zig', 'c2']]
		]);
		expect(groupMaps(mixed.filter(isOfficial)).map((g) => g.id)).toEqual(['official']);
		expect(groupMaps(mixed.filter((m) => !isOfficial(m))).map((g) => g.id)).toEqual(['community']);
		expect(groupMaps([])).toEqual([]);
	});

	it('separates the viewer\'s drafts from published maps and never crosses groups in nextMap', () => {
		const draft = { id: 'd1', name: 'My Draft', difficulty: 'easy', builtin: false, status: 'DRAFT', ownedByMe: true };
		const published = { ...community('pub', 'Published One', 'easy'), status: 'PUBLISHED', ownedByMe: false };
		const withDraft = [...mixed, draft, published];
		expect(isDraft(draft)).toBe(true);
		expect(isDraft(published)).toBe(false);
		expect(groupMaps(withDraft).map((g) => [g.id, g.maps.map((m) => m.id)])).toEqual([
			['drafts', ['d1']],
			['official', ['l1', 'l5', 'p1']],
			['community', ['zig', 'pub', 'c2']]
		]);
		expect(nextMap(withDraft, 'd1')).toBeNull();
		expect(nextMap(withDraft, 'p1')).toBeNull();
	});

	it('only the owner or an official map may be edited', () => {
		expect(canEditMap({ builtin: true })).toBe(true);
		expect(canEditMap({ builtin: false, ownedByMe: true })).toBe(true);
		expect(canEditMap({ builtin: false, ownedByMe: false })).toBe(false);
	});

	it('nextMap never leaves the group: the last official level has no next, a community map goes to the next community map', () => {
		expect(nextMap(mixed, 'l1')?.id).toBe('l5');
		expect(nextMap(mixed, 'p1')).toBeNull(); // does not fall into Claude Zigzag
		expect(nextMap(mixed, 'zig')?.id).toBe('c2');
		expect(nextMap(mixed, 'c2')).toBeNull();
	});
});

describe('nextMap', () => {
	const maps = [
		{ id: 'h', name: 'H', difficulty: 'hard' },
		{ id: 'e', name: 'E', difficulty: 'easy' },
		{ id: 'm', name: 'M', difficulty: 'medium' }
	];
	it('returns the following map in sort order, null at the end or when unknown', () => {
		expect(nextMap(maps, 'e')?.id).toBe('m');
		expect(nextMap(maps, 'm')?.id).toBe('h');
		expect(nextMap(maps, 'h')).toBeNull();
		expect(nextMap(maps, 'x')).toBeNull();
	});
});

describe('filterMaps / countByDifficulty', () => {
	const maps = [
		{ name: 'Straight Line', description: 'Walk', difficulty: 'easy' },
		{ name: 'Comb', description: 'Teeth of a comb', difficulty: 'medium' },
		{ name: 'Spiral', description: 'Recursion', difficulty: 'hard' }
	];
	it('matches name or description case-insensitively and by difficulty', () => {
		expect(filterMaps(maps, { text: '', difficulty: 'all' })).toHaveLength(3);
		expect(filterMaps(maps, { text: ' TEETH ', difficulty: 'all' }).map((m) => m.name)).toEqual(['Comb']);
		expect(filterMaps(maps, { text: 'comb', difficulty: 'hard' })).toEqual([]);
		expect(filterMaps(maps, { text: '', difficulty: 'easy' }).map((m) => m.name)).toEqual(['Straight Line']);
	});
	it('counts per difficulty', () => {
		expect(countByDifficulty(maps)).toEqual({ all: 3, easy: 1, medium: 1, hard: 1 });
	});
});

describe('splitMapName', () => {
	it('separates the series label from the title', () => {
		expect(splitMapName('Patterns 3 - Pinwheel')).toEqual({ series: 'Patterns 3', title: 'Pinwheel' });
		expect(splitMapName('Recursion 10 – Long')).toEqual({ series: 'Recursion 10', title: 'Long' });
		expect(splitMapName('My map')).toEqual({ series: null, title: 'My map' });
	});
});
