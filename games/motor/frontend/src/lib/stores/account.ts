import { writable, type Readable } from 'svelte/store';
import type { Client } from '@urql/svelte';
import { unwrap } from '$lib/api';
import { ACCOUNT_QUERY, LOGOUT_MUTATION } from '$lib/queries';
import type { Account, AuthProvider } from '$lib/types';

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
		const data = await unwrap<{ me: Account | null; authProviders: AuthProvider[] }>(
			client.query(ACCOUNT_QUERY, {}, { requestPolicy: 'network-only' })
		);
		state.set({ loaded: true, me: data.me, providers: data.authProviders });
	} catch {
		state.update((s) => ({ ...s, loaded: true, me: null }));
	}
}

/** Stores a changed account (after updateAccount). */
export function setMe(me: Account | null): void {
	state.update((s) => ({ ...s, me }));
}

/** Signs out on the server and locally. The local state is cleared even if the call fails. */
export async function signOut(client: Pick<Client, 'mutation'>): Promise<void> {
	try {
		await unwrap(client.mutation(LOGOUT_MUTATION, {}));
	} finally {
		setMe(null);
	}
}
