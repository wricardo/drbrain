import type { SessionSummary } from './types';

/** Row status label. "ready" = created but no move yet. */
export type SessionStatus = 'ready' | 'playing' | 'won' | 'lost';

/** Sessions page chips. "playing" covers every unfinished session (ready or playing). */
export type StatusFilter = 'all' | 'playing' | 'won' | 'lost';

export const STATUS_FILTERS: StatusFilter[] = ['all', 'playing', 'won', 'lost'];

export const STATUS_FILTER_LABEL: Record<StatusFilter, string> = { all: 'All', playing: 'Playing', won: 'Won', lost: 'Lost' };

/** Human status for a session row. */
export function statusText(s: Pick<SessionSummary, 'state'>): SessionStatus {
	switch (s.state.status) {
		case 'WON':
			return 'won';
		case 'LOST':
			return 'lost';
		case 'PLAYING':
			return 'playing';
		default:
			return 'ready';
	}
}

/** True once the game is over (WON or LOST): such a session can only be watched or reset. */
export function isFinished(s: Pick<SessionSummary, 'state'>): boolean {
	return s.state.status === 'WON' || s.state.status === 'LOST';
}

function statusGroup(s: Pick<SessionSummary, 'state'>): Exclude<StatusFilter, 'all'> {
	const st = statusText(s);
	return st === 'ready' ? 'playing' : st;
}

export interface SessionFilters {
	status: StatusFilter;
	/** Case-insensitive substring match on display name, session id or map name (empty = no filter). */
	text: string;
}

/** Applies the client-side filters; keeps the given order. */
export function filterSessions(list: SessionSummary[], f: SessionFilters): SessionSummary[] {
	const needle = f.text.trim().toLowerCase();
	return list.filter((s) => {
		if (f.status !== 'all' && statusGroup(s) !== f.status) return false;
		if (!needle) return true;
		return [s.displayName || 'anonymous', s.id, s.map.name].some((v) => v.toLowerCase().includes(needle));
	});
}

/** Counts sessions per status chip. */
export function countByStatus(list: SessionSummary[]): Record<StatusFilter, number> {
	const out: Record<StatusFilter, number> = { all: list.length, playing: 0, won: 0, lost: 0 };
	for (const s of list) out[statusGroup(s)]++;
	return out;
}

/** RECENT and CREATED are server orders; MOVES is applied on the client. */
export type SessionSort = 'RECENT' | 'CREATED' | 'MOVES';

/** Orders by move count (most first) for MOVES; other sorts keep the server order. Does not mutate. */
export function sortSessions(list: SessionSummary[], sort: SessionSort): SessionSummary[] {
	return sort === 'MOVES' ? [...list].sort((a, b) => b.state.moves - a.state.moves) : list;
}

