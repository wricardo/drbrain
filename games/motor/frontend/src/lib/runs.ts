import { sortMaps } from './maps';
import type { GameMap, PlayerRun } from './types';

/** One map on the "My runs" page: the map and the player's best run on it, or null when unsolved. */
export interface RunRow {
	map: GameMap;
	run: PlayerRun | null;
}

export type RunFilter = 'all' | 'solved' | 'unsolved';
export const RUN_FILTERS: RunFilter[] = ['all', 'solved', 'unsolved'];
export const RUN_FILTER_LABELS: Record<RunFilter, string> = { all: 'All maps', solved: 'Solved', unsolved: 'Not solved yet' };

/**
 * One row per map in the standard map order (easy to hard, series order), each
 * with the player's run on it. Runs for maps that no longer exist are dropped.
 */
export function buildRunRows(maps: GameMap[], runs: PlayerRun[]): RunRow[] {
	const byMap = new Map(runs.map((r) => [r.mapId, r]));
	return sortMaps(maps).map((map) => ({ map, run: byMap.get(map.id) ?? null }));
}

/** Rows matching the filter; keeps the order. */
export function filterRows(rows: RunRow[], filter: RunFilter): RunRow[] {
	if (filter === 'solved') return rows.filter((r) => r.run);
	if (filter === 'unsolved') return rows.filter((r) => !r.run);
	return rows;
}

/** Counts per filter chip. */
export function countRows(rows: RunRow[]): Record<RunFilter, number> {
	const solved = rows.filter((r) => r.run).length;
	return { all: rows.length, solved, unsolved: rows.length - solved };
}

/** Link to a player's runs by display name; null for an empty name. */
export function runsHref(name: string): string | null {
	const n = name.trim();
	return n ? `/runs?name=${encodeURIComponent(n)}` : null;
}

/** Absolute shareable link for a player's runs, or null for an empty name. */
export function shareUrl(origin: string, name: string): string | null {
	const href = runsHref(name);
	return href ? `${origin}${href}` : null;
}

/** "5 slots · 8 steps" for a run. */
export function runText(run: PlayerRun): string {
	const { slots, steps } = run.entry;
	return `${slots} ${slots === 1 ? 'slot' : 'slots'} · ${steps} ${steps === 1 ? 'step' : 'steps'}`;
}

/** "Solved 5 of 17 maps". */
export function solvedSummary(rows: RunRow[]): string {
	const { solved, all } = countRows(rows);
	return `Solved ${solved} of ${all} ${all === 1 ? 'map' : 'maps'}`;
}
