import { describe, expect, it } from 'vitest';
import { canEdit, isDraft, isPublished, mapLabel, mapStatusLabel, nextMap, prettyMapName, sortMaps } from './maps';

const maps = [
	{ mapId: 'hub_maze', name: 'hub_maze', gridSize: 20 },
	{ mapId: 'easy_trapped_park', name: 'Easy Mode - Trapped Park', gridSize: 10 },
	{ mapId: 'easy', name: 'Easy Mode', gridSize: 10 },
	{ mapId: 'classic', name: 'Classic Layout', gridSize: 15 }
];

describe('prettyMapName', () => {
	it('title-cases raw slugs and keeps real names', () => {
		expect(prettyMapName('bayou_braids')).toBe('Bayou Braids');
		expect(prettyMapName('strategic')).toBe('Strategic');
		expect(prettyMapName("Sarah's Circuit")).toBe("Sarah's Circuit");
	});
});

describe('sortMaps', () => {
	it('orders by grid size, then name', () => {
		expect(sortMaps(maps).map((m) => m.mapId)).toEqual(['easy', 'easy_trapped_park', 'classic', 'hub_maze']);
	});
	it('keeps drafts first so the owner can find a work in progress', () => {
		const withDraft = [
			...maps,
			{ mapId: 'mine', name: 'Mine', gridSize: 50, status: 'DRAFT' as const },
			{ mapId: 'big', name: 'Big', gridSize: 60, status: 'PUBLISHED' as const }
		];
		expect(sortMaps(withDraft).map((m) => m.mapId)).toEqual([
			'mine',
			'easy',
			'easy_trapped_park',
			'classic',
			'hub_maze',
			'big'
		]);
	});
	it('does not mutate its input', () => {
		const copy = [...maps];
		sortMaps(maps);
		expect(maps).toEqual(copy);
	});
});

describe('map lifecycle helpers', () => {
	it('treats only the explicit DRAFT status as a draft', () => {
		expect(isDraft({ status: 'DRAFT' })).toBe(true);
		expect(isDraft({ status: 'PUBLISHED' })).toBe(false);
		expect(isDraft({})).toBe(false);
	});

	it('treats a missing status as published for old responses', () => {
		expect(isPublished({ status: 'PUBLISHED' })).toBe(true);
		expect(isPublished({})).toBe(true);
		expect(isPublished({ status: 'DRAFT' })).toBe(false);
	});

	it('labels the state for badges', () => {
		expect(mapStatusLabel({ status: 'DRAFT' })).toBe('Draft');
		expect(mapStatusLabel({ status: 'PUBLISHED' })).toBe('Published');
		expect(mapStatusLabel({})).toBe('');
	});

	it('lets the owner or a legacy key holder edit, never a shipped map', () => {
		expect(canEdit({ ownedByMe: true })).toBe(true);
		expect(canEdit({ ownedByMe: false }, 'legacy-key')).toBe(true);
		expect(canEdit({ ownedByMe: false })).toBe(false);
		expect(canEdit({ builtin: true, ownedByMe: true }, 'legacy-key')).toBe(false);
	});
});

describe('nextMap', () => {
	it('returns the following map and wraps around', () => {
		expect(nextMap(maps, 'easy')?.mapId).toBe('easy_trapped_park');
		expect(nextMap(maps, 'hub_maze')?.mapId).toBe('easy');
	});
	it('returns null when there is nothing else to play', () => {
		expect(nextMap([maps[0]], 'hub_maze')).toBeNull();
		expect(nextMap([], 'easy')).toBeNull();
	});
});

describe('mapLabel', () => {
	it('uses the list name, falling back to the prettified id', () => {
		expect(mapLabel('classic', maps)).toBe('Classic Layout');
		expect(mapLabel('hub_maze', maps)).toBe('Hub Maze');
		expect(mapLabel('keys_run', maps)).toBe('Keys Run');
	});
});
