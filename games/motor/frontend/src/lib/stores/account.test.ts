import { get } from 'svelte/store';
import { describe, expect, it, vi } from 'vitest';
import { account, refreshAccount, setMe, signOut } from './account';
import type { Account } from '../types';

const me: Account = { handle: 'speedy_fox', displayName: 'Speedy', profilePublic: true, createdAt: '2026-01-01T00:00:00Z', handleChangeableAt: null };
const ok = <T>(data: T) => ({ toPromise: () => Promise.resolve({ data }) });
const fails = () => ({ toPromise: () => Promise.resolve({ error: new Error('offline') }) });

describe('account store', () => {
	it('starts unloaded and signed out', () => {
		expect(get(account)).toEqual({ loaded: false, me: null, providers: [] });
	});

	it('loads the player and the providers', async () => {
		const query = vi.fn(() => ok({ me, authProviders: [{ id: 'google', name: 'Google' }] }));
		await refreshAccount({ query } as never);
		expect(get(account)).toEqual({ loaded: true, me, providers: [{ id: 'google', name: 'Google' }] });
	});

	it('treats a failed lookup as signed out so guests are never blocked', async () => {
		setMe(me);
		await refreshAccount({ query: vi.fn(() => fails()) } as never);
		expect(get(account).loaded).toBe(true);
		expect(get(account).me).toBeNull();
	});

	it('works with sign-in switched off', async () => {
		await refreshAccount({ query: vi.fn(() => ok({ me: null, authProviders: [] })) } as never);
		expect(get(account)).toEqual({ loaded: true, me: null, providers: [] });
	});

	it('signs out locally even when the request fails', async () => {
		setMe(me);
		await expect(signOut({ mutation: vi.fn(() => fails()) } as never)).rejects.toThrow();
		expect(get(account).me).toBeNull();
		setMe(me);
		const mutation = vi.fn(() => ok({ logout: true }));
		await signOut({ mutation } as never);
		expect(mutation).toHaveBeenCalled();
		expect(get(account).me).toBeNull();
	});
});
