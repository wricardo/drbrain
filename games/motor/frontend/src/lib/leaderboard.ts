import type { GameMap, LeaderboardEntry, LeaderboardOrder } from './types';
import { sortMaps } from './maps';
import { profileHref } from './account';
import { runsHref } from './runs';

export interface OrderOption {
	order: LeaderboardOrder;
	/** Value used in the page URL (?order=). */
	param: string;
	label: string;
	/** One line explaining how the board is ranked. */
	rule: string;
}

/** The two boards of a map. The first is the default. */
export const ORDER_OPTIONS: OrderOption[] = [
	{
		order: 'SLOTS',
		param: 'slots',
		label: 'Fewest slots',
		rule: 'Ranked by fewest slots used, then fewest steps, then earliest.'
	},
	{
		order: 'STEPS',
		param: 'steps',
		label: 'Fewest steps',
		rule: 'Ranked by fewest steps, then fewest slots, then earliest.'
	}
];

/** Board shown when the URL names none (or an unknown one). */
export const DEFAULT_ORDER: LeaderboardOrder = 'SLOTS';

/** How many rows the page asks for (the API allows 100). */
export const PAGE_LIMIT = 100;

/** Order for a ?order= value; anything unknown is the default. */
export function parseOrder(param: string | null | undefined): LeaderboardOrder {
	return ORDER_OPTIONS.find((o) => o.param === param?.toLowerCase())?.order ?? DEFAULT_ORDER;
}

/** The ?order= value of an order. */
export function orderParam(order: LeaderboardOrder): string {
	return ORDER_OPTIONS.find((o) => o.order === order)?.param ?? 'slots';
}

/** The option for an order (falls back to the default board). */
export function orderOption(order: LeaderboardOrder): OrderOption {
	return ORDER_OPTIONS.find((o) => o.order === order) ?? ORDER_OPTIONS[0];
}

/** Page URL for a board, e.g. "/leaderboard?map=serpentine&order=steps". The default order is left out. */
export function boardHref(mapId: string, order: LeaderboardOrder = DEFAULT_ORDER): string {
	const q = new URLSearchParams({ map: mapId });
	if (order !== DEFAULT_ORDER) q.set('order', orderParam(order));
	return `/leaderboard?${q.toString()}`;
}

/**
 * The map to show: the requested one when it exists, else the first map in
 * the standard order; null when there are no maps.
 */
export function pickMap<T extends Pick<GameMap, 'id' | 'difficulty' | 'name'>>(maps: T[], requested: string | null | undefined): T | null {
	const found = requested ? maps.find((m) => m.id === requested) : undefined;
	return found ?? sortMaps(maps)[0] ?? null;
}

/**
 * True when the entry is the viewer's: it belongs to their signed-in account (the server says so),
 * or to one of this browser's own guest sessions.
 */
export function isMine(entry: Pick<LeaderboardEntry, 'sessionId'> & { mine?: boolean }, ownIds: Iterable<string>): boolean {
	if (entry.mine) return true;
	for (const id of ownIds) if (id === entry.sessionId) return true;
	return false;
}

/** Name shown for an account whose profile is private. It has no page to link to. */
export const PRIVATE_PLAYER = 'Private player';

/**
 * Where a player's name links: an account's profile, a guest's runs by name, or nowhere
 * (anonymous and private players).
 */
export function playerHref(entry: Pick<LeaderboardEntry, 'displayName' | 'handle'>): string | null {
	if (entry.handle) return profileHref(entry.handle);
	if (entry.displayName === PRIVATE_PLAYER || entry.displayName === 'Anonymous') return null;
	return runsHref(entry.displayName);
}

/** "1 slot" / "6 slots". */
export function slotsText(n: number): string {
	return n === 1 ? '1 slot' : `${n} slots`;
}

/** "1 step" / "9 steps". */
export function stepsText(n: number): string {
	return n === 1 ? '1 step' : `${n} steps`;
}

/** Tailwind classes of the rank badge: podium colours for 1-3, plain for the rest. */
export function rankBadgeClass(rank: number): string {
	switch (rank) {
		case 1:
			return 'bg-amber-100 text-amber-900 border-amber-300';
		case 2:
			return 'bg-slate-100 text-slate-800 border-slate-300';
		case 3:
			return 'bg-orange-100 text-orange-900 border-orange-300';
		default:
			return 'bg-white text-slate-700 border-slate-200';
	}
}

/** Spoken label for a rank ("1st place" ... "7th place"). */
export function rankLabel(rank: number): string {
	const mod100 = rank % 100;
	const suffix = mod100 >= 11 && mod100 <= 13 ? 'th' : ({ 1: 'st', 2: 'nd', 3: 'rd' } as Record<number, string>)[rank % 10] ?? 'th';
	return `${rank}${suffix} place`;
}

/**
 * The compact line shown after a win: "Rank 2 of 14 · 6 slots · 9 steps" for the
 * default board. Null when the session has no entry.
 */
export function rankSummary(yours: LeaderboardEntry | null, total: number): string | null {
	if (!yours) return null;
	return `Rank ${yours.rank} of ${total} · ${slotsText(yours.slots)} · ${stepsText(yours.steps)}`;
}
