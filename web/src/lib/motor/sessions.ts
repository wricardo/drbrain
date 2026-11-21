import { lossReasonText } from '@drbrain/ui/errors';
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
	/** Case-insensitive substring match on session id or map name (empty = no filter). */
	text: string;
}

/** Applies the client-side filters; keeps the input order. */
export function filterSessions(list: SessionSummary[], f: SessionFilters): SessionSummary[] {
	const needle = f.text.trim().toLowerCase();
	return list.filter((s) => {
		if (f.status !== 'all' && statusGroup(s) !== f.status) return false;
		if (needle && !s.id.toLowerCase().includes(needle) && !s.map.name.toLowerCase().includes(needle))
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
	/** The Main tape is empty: a ready robot has nothing to run yet. */
	emptyMain?: boolean;
}): string {
	if (s.vm.status === 'WON') return `Won in ${s.vm.steps} steps`;
	if (s.vm.status === 'LOST') return `Lost: ${lossReasonText(s.vm.lossReason)}`;
	if (s.playing) return s.speedMs ? `Running at ${s.speedMs} ms/step` : 'Running';
	if (s.vm.status === 'READY') return s.emptyMain ? 'Add an instruction to Main to start' : 'Ready';
	return 'Paused';
}

/**
 * The browser tab title for a session page, so a run on another tab can be followed from the tab strip:
 * "▶ Map" while it plays, "✓ Map" once won, "✗ Map" once lost, the plain name otherwise.
 */
export function runTitle(mapName: string | undefined, s: { playing: boolean; status: Status } | null): string | undefined {
	if (!mapName || !s) return mapName;
	if (s.status === 'WON') return `✓ Won · ${mapName}`;
	if (s.status === 'LOST') return `✗ Lost · ${mapName}`;
	if (s.playing) return `▶ ${mapName}`;
	return mapName;
}

/**
 * The tab title for the spectator page. It says "Watching" while there is nothing to report, and
 * the run state once there is one, so a finished run does not read "✓ Won · Watching Belt Run".
 */
export function watchTitle(mapName: string | undefined, s: { playing: boolean; status: Status } | null): string | undefined {
	if (!mapName) return mapName;
	if (s && (s.status === 'WON' || s.status === 'LOST')) return runTitle(mapName, s);
	if (s?.playing) return `▶ Watching ${mapName}`;
	return `Watching ${mapName}`;
}

const LAST_SESSION_KEY = 'drbrain-motor-programming.lastSession';

const OWN_SESSIONS_KEY = 'drbrain-motor-programming.ownSessions';
/** How many session ids this browser remembers as its own (newest kept). */
export const MAX_OWN_SESSIONS = 200;

/**
 * Remembers the session this browser created most recently (for the home
 * "Continue" card) and adds it to the list of sessions this browser owns (so
 * the leaderboard can highlight its entries and "My runs" can find them).
 */
export function rememberSession(id: string, ownerToken?: string | null): void {
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

/**
 * The owner token for one session, or null. It proves this browser created the
 * session: it is what setProgram (and the program in any response) needs, while
 * the session id alone is public and enough to watch or drive playback. Null for
 * a session this browser did not create — a signed-in player's session does not
 * need it either, because their login cookie proves it.
 */
export function ownerTokenFor(id: string): string | null {
	return ownerTokens()[id] ?? null;
}

/**
 * Whether this browser started the session, and so may change its program. A
 * signed-in account edits its own sessions with the cookie instead, which this
 * cannot see; the play page asks the server (`canPlaySession`) when it matters.
 */
export function ownsSession(id: string): boolean {
	return ownerTokenFor(id) !== null;
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
