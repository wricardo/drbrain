// Browser-side memory of the Delivery sessions this browser created, and the secret
// owner tokens that prove it. Every function tolerates unavailable storage and
// corrupt JSON: losing this memory only costs the Continue card and later claiming.

const LAST_SESSION_KEY = 'drbrain-delivery.lastSession';
const OWN_SESSIONS_KEY = 'drbrain-delivery.ownSessions';
const OWNER_TOKENS_KEY = 'drbrain-delivery.ownerTokens';

/** How many session ids this browser remembers as its own (newest kept). */
export const MAX_OWN_SESSIONS = 200;

/**
 * Remembers the session this browser created most recently (for a "Continue"
 * card) and adds it to the list of sessions this browser owns. A token, when the
 * server returned one, is stored too so the session can be claimed later.
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
		// storage unavailable: own sessions just won't be recognised
	}
}

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

/** Owner tokens remembered by this browser, by session id (oldest first); {} when none, corrupt, or unavailable. */
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
 * session, while the id alone is public. Null for a session this browser did not create.
 */
export function ownerTokenFor(id: string): string | null {
	return ownerTokens()[id] ?? null;
}

/** What to send to a claim mutation: this browser's sessions that have an owner token (newest max). */
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

/** Ids of the sessions created in this browser (oldest first); [] when none, corrupt, or storage is unavailable. */
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
