import type { Client } from '@urql/svelte';
import { unwrap } from '@drbrain/ui/graphql';
import { CREATE_SESSION_MUTATION } from './queries';
import { rememberOwnerToken } from './ownerTokens';
import type { Session } from './types';

/** localStorage key of the player name typed on the home page. */
export const NAME_KEY = 'drbrain-mazes.displayName';

/** The player name this browser last used ('' when none). */
export function savedName(): string {
	try {
		return typeof localStorage === 'undefined' ? '' : (localStorage.getItem(NAME_KEY) ?? '');
	} catch {
		return '';
	}
}

/**
 * Creates a session on a map for the given player name, remembers the name and the secret owner
 * token (the proof this browser may move the miner) and returns the new session id.
 */
export async function startSession(client: Client, mapID: string, displayName: string): Promise<string> {
	const name = displayName.trim();
	try {
		localStorage.setItem(NAME_KEY, name);
	} catch {
		// Storage blocked: the name is only a convenience.
	}
	const data = await unwrap<{ createSession: { session: Session; ownerToken: string } }>(
		client.mutation(CREATE_SESSION_MUTATION, { mapID, displayName: name || null })
	);
	rememberOwnerToken(data.createSession.session.id, data.createSession.ownerToken);
	return data.createSession.session.id;
}
