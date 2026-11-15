import { lossReasonText } from './errors';
import type { GameMap, LossReason, SessionSummary, Status } from './types';

export type SessionStatus = 'running' | 'ready' | 'paused' | 'won' | 'lost';
export type StatusFilter = 'all' | 'playing' | 'won' | 'lost';

export const STATUS_FILTERS: StatusFilter[] = ['all', 'playing', 'won', 'lost'];
export const STATUS_FILTER_LABELS: Record<StatusFilter, string> = { all: 'All', playing: 'Playing', won: 'Won', lost: 'Lost' };

/** Human status for a session row: won/lost win over playing; READY idle is "ready", otherwise "paused". */
export function statusText(s: Pick<SessionSummary, 'playing' | 'vm'>): SessionStatus {
	if (s.vm.status === 'WON') return 'won';
	if (s.vm.status === 'LOST') return 'lost';
	if (s.playing) return 'running';
	return s.vm.status === 'READY' ? 'ready' : 'paused';
}

/** Filter group of a session: anything not finished counts as "playing". */
export function statusGroup(s: Pick<SessionSummary, 'playing' | 'vm'>): Exclude<StatusFilter, 'all'> {
	const st = statusText(s);
	return st === 'won' || st === 'lost' ? st : 'playing';
}

/** True when the session has finished (won or lost). */
export function isFinished(s: Pick<SessionSummary, 'vm'>): boolean {
	return s.vm.status === 'WON' || s.vm.status === 'LOST';
}

export type SessionSort = 'recent' | 'created' | 'steps';
export const SESSION_SORTS: [SessionSort, string][] = [
	['recent', 'Last activity'],
	['created', 'Newest created'],
	['steps', 'Most steps']
];

export interface SessionFilters {
	status: StatusFilter;
	/** Case-insensitive substring match on display name, session id or map name (empty = no filter). */
	text: string;
}

/** Applies the client-side filters; keeps the input order. */
export function filterSessions(list: SessionSummary[], f: SessionFilters): SessionSummary[] {
	const needle = f.text.trim().toLowerCase();
	return list.filter((s) => {
		if (f.status !== 'all' && statusGroup(s) !== f.status) return false;
		if (
			needle &&
			!(s.displayName || 'anonymous').toLowerCase().includes(needle) &&
			!s.id.toLowerCase().includes(needle) &&
			!s.map.name.toLowerCase().includes(needle)
		)
			return false;
		return true;
	});
}

/** Sorts sessions client-side (newest first; ties keep input order). Does not mutate. */
export function sortSessions(list: SessionSummary[], sort: SessionSort): SessionSummary[] {
	const t = (iso: string) => new Date(iso).getTime() || 0;
	const key = (s: SessionSummary) =>
		sort === 'steps' ? s.vm.steps : sort === 'created' ? t(s.createdAt) : t(s.lastActionAt);
	return [...list].sort((a, b) => key(b) - key(a));
}

/** Counts sessions per filter chip. */
export function countByStatus(list: SessionSummary[]): Record<StatusFilter, number> {
	const out: Record<StatusFilter, number> = { all: list.length, playing: 0, won: 0, lost: 0 };
	for (const s of list) out[statusGroup(s)]++;
	return out;
}

/** Sessions that have actually started (at least one step), newest activity first, at most max. */
export function recentStarted(list: SessionSummary[], max = 8): SessionSummary[] {
	return sortSessions(
		list.filter((s) => s.vm.steps > 0),
		'recent'
	).slice(0, max);
}

/**
 * The status line shown at the top of the play/watch side panel:
 * Ready / Running (at N ms/step) / Paused / Won in N steps / Lost: reason.
 */
export function statusLine(s: {
	playing: boolean;
	speedMs?: number;
	vm: { status: Status; steps: number; lossReason: LossReason | null };
}): string {
	if (s.vm.status === 'WON') return `Won in ${s.vm.steps} steps`;
	if (s.vm.status === 'LOST') return `Lost: ${lossReasonText(s.vm.lossReason)}`;
	if (s.playing) return s.speedMs ? `Running at ${s.speedMs} ms/step` : 'Running';
	return s.vm.status === 'READY' ? 'Ready' : 'Paused';
}

const LAST_SESSION_KEY = 'drbrain-motor-programming.lastSession';

const OWN_SESSIONS_KEY = 'drbrain-motor-programming.ownSessions';
/** How many session ids this browser remembers as its own (newest kept). */
export const MAX_OWN_SESSIONS = 200;

/**
 * Remembers the session this browser created most recently (for the home
 * "Continue" card) and adds it to the list of sessions this browser owns (so
 * the leaderboard can highlight its entries and "My runs" can find them).
 *
 * When the player had no name the server gave the session a random guest name
 * ("BraveOtter"); pass it as `name` and it becomes this browser's saved name,
 * so every later session is the same guest. A name the player typed is never
 * replaced.
 */
export function rememberSession(id: string, name?: string, ownerToken?: string | null): void {
	if (name?.trim() && !savedDisplayName().trim()) saveDisplayName(name.trim());
	if (ownerToken) rememberOwnerToken(id, ownerToken);
	try {
		localStorage.setItem(LAST_SESSION_KEY, id);
	} catch {
		// storage unavailable: the Continue card just won't show
	}
	try {
		const ids = ownSessionIds().filter((x) => x !== id);
		ids.push(id);
		localStorage.setItem(OWN_SESSIONS_KEY, JSON.stringify(ids.slice(-MAX_OWN_SESSIONS)));
	} catch {
		// storage unavailable: no highlighting
	}
}

const OWNER_TOKENS_KEY = 'drbrain-motor-programming.ownerTokens';

/**
 * Remembers the secret owner token the server returned when this browser created
 * a session. A session id is public (spectator links), so the token is what lets
 * a player who signs in later attach this browser's guest runs to their account.
 * Only the newest MAX_OWN_SESSIONS are kept.
 */
export function rememberOwnerToken(id: string, token: string): void {
	try {
		const all = ownerTokens();
		delete all[id];
		all[id] = token; // object keys keep insertion order: newest last
		const keep = Object.entries(all).slice(-MAX_OWN_SESSIONS);
		localStorage.setItem(OWNER_TOKENS_KEY, JSON.stringify(Object.fromEntries(keep)));
	} catch {
		// storage unavailable: the runs just cannot be attached later
	}
}

/** Owner tokens remembered by this browser, by session id. */
export function ownerTokens(): Record<string, string> {
	try {
		const raw = localStorage.getItem(OWNER_TOKENS_KEY);
		const parsed: unknown = raw ? JSON.parse(raw) : {};
		if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return {};
		const out: Record<string, string> = {};
		for (const [k, v] of Object.entries(parsed)) if (typeof v === 'string' && v) out[k] = v;
		return out;
	} catch {
		return {};
	}
}

/** What to send to claimSessions: this browser's sessions that have an owner token (at most max). */
export function claimableSessions(max = 100): { sessionID: string; ownerToken: string }[] {
	return Object.entries(ownerTokens())
		.slice(-max)
		.map(([sessionID, ownerToken]) => ({ sessionID, ownerToken }));
}

/** Forgets the tokens of the given sessions (after they were attached, or refused). */
export function forgetOwnerTokens(ids: string[]): void {
	try {
		const all = ownerTokens();
		for (const id of ids) delete all[id];
		localStorage.setItem(OWNER_TOKENS_KEY, JSON.stringify(all));
	} catch {
		// ignore
	}
}

/** Forgets every owner token (when the player deletes their account on this browser). */
export function forgetAllOwnerTokens(): void {
	try {
		localStorage.removeItem(OWNER_TOKENS_KEY);
	} catch {
		// ignore
	}
}

/** Ids of the sessions created in this browser (oldest first); [] when none or storage is unavailable. */
export function ownSessionIds(): string[] {
	try {
		const raw = localStorage.getItem(OWN_SESSIONS_KEY);
		const parsed: unknown = raw ? JSON.parse(raw) : [];
		return Array.isArray(parsed) ? parsed.filter((x): x is string => typeof x === 'string') : [];
	} catch {
		return [];
	}
}

/** The session id saved by rememberSession, or null. */
export function lastSessionId(): string | null {
	try {
		return localStorage.getItem(LAST_SESSION_KEY);
	} catch {
		return null;
	}
}

/** Forgets the remembered session (e.g. it was deleted or finished). */
export function forgetSession(): void {
	try {
		localStorage.removeItem(LAST_SESSION_KEY);
	} catch {
		// ignore
	}
}

/**
 * "8 / 70 steps · 1 / 8 treats" for list rows. Limits come from the map list
 * (the sessions query stays lean to fit the 500-row complexity budget); without
 * the map it falls back to "8 steps · 7 treats left".
 */
export function progressText(
	s: Pick<SessionSummary, 'vm'>,
	map?: Pick<GameMap, 'maxSteps' | 'treats'> | null
): string {
	if (!map) return `${s.vm.steps} steps · ${s.vm.treatsRemaining.length} treats left`;
	const total = map.treats.length;
	return `${s.vm.steps} / ${map.maxSteps} steps · ${total - s.vm.treatsRemaining.length} / ${total} treats`;
}

const NAME_KEY = 'drbrain-motor-programming.displayName';
/** Key used before the repo rename; read as a fallback so saved names survive. */
const LEGACY_NAME_KEY = 'drbrain3.displayName';

/** The player name saved on the home page ('' when none). */
export function savedDisplayName(): string {
	try {
		return localStorage.getItem(NAME_KEY) ?? localStorage.getItem(LEGACY_NAME_KEY) ?? '';
	} catch {
		return '';
	}
}

/** Saves the player name used for new sessions. */
export function saveDisplayName(name: string): void {
	try {
		localStorage.setItem(NAME_KEY, name);
		localStorage.removeItem(LEGACY_NAME_KEY);
	} catch {
		// ignore
	}
}
