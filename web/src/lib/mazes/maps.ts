import type { GameMap, MapStatus } from './types';

const RANK: Record<string, number> = { easy: 0, medium: 1, hard: 2 };

/** True for the caller's unpublished work in progress. */
export function isDraft(map: { status?: MapStatus }): boolean {
	return map.status === 'DRAFT';
}

/** True for a map everyone can see and play. Absent status counts as published (legacy responses). */
export function isPublished(map: { status?: MapStatus }): boolean {
	return !isDraft(map);
}

/** A word for the map's state, or "" when the response predates the lifecycle fields. */
export function mapStatusLabel(map: { status?: MapStatus }): '' | 'Draft' | 'Published' {
	if (!map.status) return '';
	return isDraft(map) ? 'Draft' : 'Published';
}

/**
 * True when this caller may change the map: its owner, or an admin (shipped
 * and legacy ownerless maps are admin-only). `hasAdminKey` is only the intent
 * to act as admin; the server verifies the key.
 */
export function canEditMap(map: { ownedByMe?: boolean }, hasAdminKey = false): boolean {
	return map.ownedByMe === true || hasAdminKey;
}

/**
 * The one map order used everywhere (home, maps page, dropdowns, "Next map"):
 * the caller's drafts first while they are unpublished, then easy -> medium ->
 * hard, then by name (unknown difficulties last). Does not mutate the input.
 */
export function sortMapsByDifficulty<T extends Pick<GameMap, 'difficulty' | 'name'> & { status?: MapStatus }>(
	maps: T[]
): T[] {
	const rank = (m: T) => RANK[m.difficulty] ?? 99;
	return [...maps].sort(
		(a, b) => Number(isDraft(b)) - Number(isDraft(a)) || rank(a) - rank(b) || a.name.localeCompare(b.name)
	);
}

export type DifficultyFilter = 'all' | 'easy' | 'medium' | 'hard';

export const DIFFICULTY_FILTERS: DifficultyFilter[] = ['all', 'easy', 'medium', 'hard'];

type FilterableMap = Pick<GameMap, 'difficulty' | 'name' | 'description'>;

/** Keeps maps of the chosen difficulty whose name or description contains the text (case-insensitive). */
export function filterMaps<T extends FilterableMap>(maps: T[], f: { difficulty: DifficultyFilter; text: string }): T[] {
	const needle = f.text.trim().toLowerCase();
	return maps.filter(
		(m) =>
			(f.difficulty === 'all' || m.difficulty === f.difficulty) &&
			(!needle || m.name.toLowerCase().includes(needle) || m.description.toLowerCase().includes(needle))
	);
}

/** Map count per difficulty chip ("all" is the total). */
export function countByDifficulty(maps: Pick<GameMap, 'difficulty'>[]): Record<DifficultyFilter, number> {
	const out: Record<DifficultyFilter, number> = { all: maps.length, easy: 0, medium: 0, hard: 0 };
	for (const m of maps) if (m.difficulty in out && m.difficulty !== 'all') out[m.difficulty as DifficultyFilter]++;
	return out;
}

/** The map after `id` in the shared order; null for the last map or an unknown id. */
export function nextMap<T extends Pick<GameMap, 'id' | 'difficulty' | 'name'> & { status?: MapStatus }>(
	maps: T[],
	id: string
): T | null {
	const sorted = sortMapsByDifficulty(maps);
	const i = sorted.findIndex((m) => m.id === id);
	return i >= 0 && i + 1 < sorted.length ? sorted[i + 1] : null;
}
