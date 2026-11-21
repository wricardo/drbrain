/**
 * Legacy edit tokens.
 *
 * Maps created since accounts became required are owned by the signed-in
 * player and need no token. Only a map created before that change is
 * ownerless, and the token the server returned once from `createMap` is the
 * proof `claimMap` accepts to attach it to an account (until then the token
 * also authorizes updateMap/deleteMap).
 *
 * Tokens are never dropped silently: losing one means the map can only be
 * changed by an admin, so an entry is forgotten only after a successful claim
 * or a delete.
 */
const STORAGE_KEY = 'tesla-road-trip.mapEditKeys';

type KeyMap = Record<string, string>;

/**
 * Storage lookup: prefer window.localStorage, which is also what jsdom/browsers
 * expose. (Node 2x ships a global `localStorage` that is inert unless a flag is
 * passed, so it must not be used directly in tests or SSR.)
 */
function storage(): Storage | null {
	if (typeof window === 'undefined') return null;
	try {
		return window.localStorage ?? null;
	} catch {
		// Access can throw when storage is disabled by policy.
		return null;
	}
}

function readAll(): KeyMap {
	const store = storage();
	if (!store) return {};
	try {
		const raw = store.getItem(STORAGE_KEY);
		if (!raw) return {};
		const parsed = JSON.parse(raw);
		return parsed && typeof parsed === 'object' ? (parsed as KeyMap) : {};
	} catch {
		return {};
	}
}

function writeAll(keys: KeyMap) {
	const store = storage();
	if (!store) return;
	try {
		store.setItem(STORAGE_KEY, JSON.stringify(keys));
	} catch {
		// Storage can be full or blocked (private mode); editing still works for
		// this session because the caller keeps the token in memory.
	}
}

/** Returns the stored legacy edit token for a map, or null. */
export function getMapEditKey(mapId: string): string | null {
	return readAll()[mapId] ?? null;
}

/** Stores a legacy edit token (only the server's createMap transfer path returns one). */
export function setMapEditKey(mapId: string, editKey: string) {
	if (!mapId || !editKey) return;
	const keys = readAll();
	keys[mapId] = editKey;
	writeAll(keys);
}

/**
 * Forgets the token for a map after it is deleted, or after claimMap attached
 * the map to an account (ownership replaces the token).
 */
export function forgetMapEditKey(mapId: string) {
	const keys = readAll();
	if (!(mapId in keys)) return;
	delete keys[mapId];
	writeAll(keys);
}

/** True when this browser holds a legacy token that may claim the map. */
export function canEditMap(mapId: string): boolean {
	return Boolean(getMapEditKey(mapId));
}

/**
 * Turns a GraphQL error into a message the user can act on. The server's
 * authorization and validation errors are already actionable, so they pass
 * through unchanged; this is the single place to soften wording later.
 */
export function mapSaveErrorMessage(error: unknown): string {
	const message = error instanceof Error ? error.message : String(error ?? 'Unknown error');
	return message;
}
