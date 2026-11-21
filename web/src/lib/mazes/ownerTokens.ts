// Browser-side memory of the owner tokens for the Mazes sessions this browser
// created. The session id is public (it is the watch link), so the token is
// what proves the right to move the miner. Every function tolerates unavailable
// storage and corrupt JSON: losing this memory only costs the ability to play.

const OWNER_TOKENS_KEY = 'drbrain-mazes.ownerTokens';

/** How many owner tokens this browser keeps (newest kept). */
export const MAX_OWN_SESSIONS = 200;

/** Remembers the secret token returned when this browser created a session. */
export function rememberOwnerToken(id: string, token: string): void {
	try {
		const all = ownerTokens();
		delete all[id];
		all[id] = token; // object keys keep insertion order: newest last
		const keep = Object.entries(all).slice(-MAX_OWN_SESSIONS);
		localStorage.setItem(OWNER_TOKENS_KEY, JSON.stringify(Object.fromEntries(keep)));
	} catch {
		// storage unavailable: the session can still be watched, just not played
	}
}

/** Owner tokens remembered by this browser, by session id (oldest first); {} when none. */
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

/** The owner token for one session, or null when this browser did not create it. */
export function ownerTokenFor(id: string): string | null {
	return ownerTokens()[id] ?? null;
}
