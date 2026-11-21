import type { CellConfigEntry } from '$lib/tesla/directional';
import type { Tesla_MapStatus as MapStatus } from '$lib/graphql-types';

export type MapSummary = {
	mapId: string;
	name: string;
	description: string;
	gridSize: number;
	maxBattery: number;
	builtin?: boolean;
	status?: MapStatus;
	ownedByMe?: boolean;
};

export type { MapStatus };

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
 * True when the caller may change the map: their own community map, or a
 * legacy ownerless map this browser still holds the one-time edit key for.
 * Shipped maps are admin-only and are not editable here.
 */
export function canEdit(map: { builtin?: boolean; ownedByMe?: boolean }, legacyEditKey: string | null = null): boolean {
	if (map.builtin) return false;
	return map.ownedByMe === true || Boolean(legacyEditKey);
}

export type LegendEntry = { key: string; value: string };

export type MapLayout = {
	name: string;
	description: string;
	gridSize: number;
	maxBattery: number;
	startingBattery: number;
	layout: string[];
	legend: LegendEntry[];
	cellConfigs: CellConfigEntry[];
};

/** Human label for a map: raw slugs ("bayou_braids") become "Bayou Braids". */
export function prettyMapName(name: string): string {
	const n = (name ?? '').trim();
	if (!n) return '';
	return n.includes('_') || n === n.toLowerCase()
		? n.replace(/_/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase())
		: n;
}

/** Display name for a map id, using the maps list when available. */
export function mapLabel(mapId: string, maps: Pick<MapSummary, 'mapId' | 'name'>[] = []): string {
	const m = maps.find((x) => x.mapId === mapId);
	return prettyMapName(m?.name || mapId);
}

/**
 * The one map order used everywhere (home, maps page, next map): the caller's
 * drafts first while they are unpublished, then grid size (small → large),
 * then name. Drafts are only ever returned to their owner, so this keeps a
 * work in progress in reach without hiding it among the shipped maps.
 */
export function sortMaps<T extends Pick<MapSummary, 'mapId' | 'name' | 'gridSize'> & { status?: MapStatus }>(
	maps: T[]
): T[] {
	return [...maps].sort(
		(a, b) =>
			Number(isDraft(b)) - Number(isDraft(a)) ||
			a.gridSize - b.gridSize ||
			prettyMapName(a.name).localeCompare(prettyMapName(b.name)) ||
			a.mapId.localeCompare(b.mapId)
	);
}

/** Next map after `mapId` in sortMaps order (wraps to the first), or null. */
export function nextMap<T extends Pick<MapSummary, 'mapId' | 'name' | 'gridSize'> & { status?: MapStatus }>(
	maps: T[],
	mapId: string
): T | null {
	const sorted = sortMaps(maps);
	if (sorted.length === 0) return null;
	const i = sorted.findIndex((m) => m.mapId === mapId);
	const next = sorted[(i + 1) % sorted.length];
	return next && next.mapId !== mapId ? next : null;
}

export function tileType(char: string, legend: LegendEntry[] = [], cellConfigs: CellConfigEntry[] = []): string {
	return cellConfigs.find((entry) => entry.key === char)?.type ?? legend.find((entry) => entry.key === char)?.value ?? 'road';
}

export function tileClass(type: string): string {
	switch (type) {
		case 'home': return 'bg-red-500';
		case 'park': return 'bg-emerald-500';
		case 'supercharger': return 'bg-yellow-400';
		case 'water': return 'bg-blue-400';
		case 'building': return 'bg-slate-700';
		default: return 'bg-white';
	}
}

export function countTiles(map: MapLayout | undefined, type: string): number {
	if (!map) return 0;
	let n = 0;
	for (const row of map.layout) for (const ch of row) if (tileType(ch, map.legend, map.cellConfigs) === type) n++;
	return n;
}
