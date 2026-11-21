import { writable, type Readable } from 'svelte/store';
import type { Client } from '@urql/svelte';
import { unwrap } from '@drbrain/ui/graphql'
import { accountRequestContext, ACCOUNT_QUERY, LOGOUT_MUTATION } from '$lib/account/queries';
import { clearAuthToken } from '$lib/account/token';
import type { Account, AuthProvider } from '$lib/account/types';

export interface AccountState {
	/** False until the first lookup has finished (so the header does not flash "Sign in"). */
	loaded: boolean;
	/** The signed-in player, or null. */
	me: Account | null;
	/** Sign-in providers this server offers; empty when sign-in is off. */
	providers: AuthProvider[];
}

const state = writable<AccountState>({ loaded: false, me: null, providers: [] });

/** The signed-in player and the sign-in options. */
export const account: Readable<AccountState> = { subscribe: state.subscribe };

/** Looks up the signed-in player and the providers. A failure counts as signed out: guests are never blocked. */
export async function refreshAccount(client: Pick<Client, 'query'>): Promise<void> {
	try {
		const data = await unwrap<{ account: { me: Account | null; authProviders: AuthProvider[] } }>(
			client.query(ACCOUNT_QUERY, {}, { requestPolicy: 'network-only' })
		);
		state.set({ loaded: true, me: data.account.me, providers: data.account.authProviders });
	} catch {
		state.update((s) => ({ ...s, loaded: true, me: null }));
	}
}

/** Stores a changed account (after updateAccount). */
export function setMe(me: Account | null): void {
	state.update((s) => ({ ...s, me }));
}

/** Signs out locally and tells the server (which has nothing to revoke). The
 * token is cleared even when the call fails. */
export async function signOut(client: Pick<Client, 'mutation'>): Promise<void> {
	clearAuthToken();
	try {
		await unwrap(client.mutation(LOGOUT_MUTATION, {}, accountRequestContext));
	} finally {
		setMe(null);
	}
}
