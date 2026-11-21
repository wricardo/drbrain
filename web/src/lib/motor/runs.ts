import { isOfficial, sortMaps } from './maps';
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

/** "5 slots · 8 steps" for a run. */
export function runText(run: PlayerRun): string {
	const { slots, steps } = run.entry;
	return `${slots} ${slots === 1 ? 'slot' : 'slots'} · ${steps} ${steps === 1 ? 'step' : 'steps'}`;
}

/**
 * "Solved 5 of 18 maps". Once community maps exist the official campaign is counted on
 * its own, so the headline number does not move when a player makes a map:
 * "Solved 5 of 18 official maps · 1 of 2 community maps".
 */
export function solvedSummary(rows: RunRow[]): string {
	const plural = (n: number, word: string) => `${n} ${word}${n === 1 ? '' : 's'}`;
	const official = rows.filter((r) => isOfficial(r.map));
	const community = rows.filter((r) => !isOfficial(r.map));
	const o = countRows(official);
	if (community.length === 0) return `Solved ${o.solved} of ${plural(o.all, 'map')}`;
	const c = countRows(community);
	return `Solved ${o.solved} of ${plural(o.all, 'official map')} · ${c.solved} of ${plural(c.all, 'community map')}`;
}
