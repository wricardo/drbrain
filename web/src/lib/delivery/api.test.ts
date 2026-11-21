import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { api } from './api';
import { ownerTokenFor, ownerTokens, rememberOwnerToken } from './ownerTokens';

type Call = { query: string; variables: Record<string, unknown> };
let calls: Call[];

/** Replaces fetch with one that records the operation and answers { data: { delivery } }. */
function mockFetch(delivery: unknown) {
	calls = [];
	vi.stubGlobal(
		'fetch',
		vi.fn(async (_url: string, init: RequestInit) => {
			calls.push(JSON.parse(init.body as string));
			return new Response(JSON.stringify({ data: { delivery } }), { status: 200 });
		})
	);
}

beforeEach(() => localStorage.clear());
afterEach(() => vi.unstubAllGlobals());

describe('delivery api owner tokens', () => {
	it('createSession sends the mutation and remembers the returned token', async () => {
		mockFetch({ createSession: { session: { id: 's1', cars: [] }, ownerToken: 'tok-1' } });
		const snap = await api.createSession();
		expect(snap.id).toBe('s1');
		expect(calls[0].query).toContain('createSession(id: $id, seed: $seed)');
		expect(calls[0].query).toContain('ownerToken');
		expect(ownerTokenFor('s1')).toBe('tok-1');
	});

	it('a gated mutation sends the stored ownerToken', async () => {
		rememberOwnerToken('s1', 'tok-1');
		mockFetch({ tick: { id: 's1' } });
		await api.tick('s1', 2);
		expect(calls[0].query).toContain('$ownerToken: String');
		expect(calls[0].query).toContain('tick(id: $id, ownerToken: $ownerToken');
		expect(calls[0].variables).toMatchObject({ id: 's1', ownerToken: 'tok-1', count: 2 });
	});

	it('a gated mutation sends null for a session without a token', async () => {
		mockFetch({ setSpeed: { id: 'legacy' } });
		await api.setSpeed('legacy', 1);
		expect(calls[0].variables.ownerToken).toBeNull();
	});

	it('sessions selects the owner fields', async () => {
		mockFetch({ sessions: [] });
		await api.sessions();
		expect(calls[0].query).toContain('ownerDisplayName ownerHandle mine');
	});

	it('deleteSession forgets the token', async () => {
		rememberOwnerToken('s1', 'tok-1');
		rememberOwnerToken('s2', 'tok-2');
		mockFetch({ deleteSession: true });
		await api.deleteSession('s1');
		expect(calls[0].variables.ownerToken).toBe('tok-1');
		expect(ownerTokens()).toEqual({ s2: 'tok-2' });
	});

	it('deleteSession keeps the token when the server says no', async () => {
		rememberOwnerToken('s1', 'tok-1');
		mockFetch({ deleteSession: false });
		await api.deleteSession('s1');
		expect(ownerTokenFor('s1')).toBe('tok-1');
	});

	it('reads send no token', async () => {
		rememberOwnerToken('s1', 'tok-1');
		mockFetch({ session: { id: 's1' }, carView: {}, events: [] });
		await api.session('s1');
		await api.carView('s1', 'c1');
		await api.events('s1');
		for (const c of calls) {
			expect(c.query).not.toContain('ownerToken');
			expect(c.variables).not.toHaveProperty('ownerToken');
		}
	});

	it('canPlay asks canPlaySession with the stored token', async () => {
		rememberOwnerToken('s1', 'tok-1');
		mockFetch({ canPlaySession: true });
		expect(await api.canPlay('s1')).toBe(true);
		expect(calls[0].query).toContain('canPlaySession(id: $id, ownerToken: $ownerToken)');
		expect(calls[0].variables).toEqual({ id: 's1', ownerToken: 'tok-1' });
	});

	it('claimSessions sends the claims and returns the results', async () => {
		const results = [{ sessionID: 's1', status: 'ATTACHED' }];
		mockFetch({ claimSessions: results });
		const claims = [{ sessionID: 's1', ownerToken: 'tok-1' }];
		expect(await api.claimSessions(claims)).toEqual(results);
		expect(calls[0].variables).toEqual({ claims });
	});
});
