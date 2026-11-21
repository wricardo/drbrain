import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { api, ApiError } from './api';
import { setAdminKey } from './admin';
import { ownerTokenFor, ownerTokens, rememberOwnerToken } from './ownerTokens';

type Call = { query: string; variables: Record<string, unknown>; headers: Record<string, string> };
let calls: Call[];

/** Replaces fetch with one that records the operation and answers { data: { delivery } }. */
function mockFetch(delivery: unknown) {
	calls = [];
	vi.stubGlobal(
		'fetch',
		vi.fn(async (_url: string, init: RequestInit) => {
			calls.push({
				...JSON.parse(init.body as string),
				headers: (init.headers ?? {}) as Record<string, string>
			});
			return new Response(JSON.stringify({ data: { delivery } }), { status: 200 });
		})
	);
}

beforeEach(() => {
	localStorage.clear();
	sessionStorage.clear();
});
afterEach(() => vi.unstubAllGlobals());

describe('delivery api owner tokens', () => {
	it('createSession sends the mutation, city id and remembers the returned token', async () => {
		mockFetch({ createSession: { session: { id: 's1', cars: [] }, ownerToken: 'tok-1' } });
		const snap = await api.createSession(undefined, undefined, 'alpha');
		expect(snap.id).toBe('s1');
		expect(calls[0].query).toContain('createSession(id: $id, seed: $seed, cityId: $cityId)');
		expect(calls[0].query).toContain('ownerToken');
		expect(calls[0].variables).toMatchObject({ cityId: 'alpha' });
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

	it('session snapshots request the monthly report and category on each row', async () => {
		mockFetch({ session: { id: 's1' } });
		await api.session('s1');
		const query = calls[0].query;
		expect(query).toContain('ticksPerMonth');
		expect(query).toContain('months {');
		expect(query).toMatch(/index startTick endTick income expenses net/);
		expect(query).toMatch(/categories \{ category income expenses \}/);
		expect(query).toMatch(/cashEntries \{ seq tick kind category amount balance/);
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

describe('delivery city api', () => {
	const view = {
		id: 'alpha',
		name: 'Alpha',
		gridSize: 5,
		status: 'DRAFT',
		ownedByMe: true,
		rows: ['.....'],
		hiddenTiles: 0,
		kitchens: [],
		customers: [],
		depots: [],
		chargers: []
	};

	it('lists and fetches cities through the delivery namespace', async () => {
		mockFetch({
			cities: [{ id: 'city', name: 'City', gridSize: 8, status: 'PUBLISHED', ownedByMe: false }],
			city: view
		});
		expect(await api.cities()).toHaveLength(1);
		expect(calls[0].query).toContain('cities { id name gridSize status ownedByMe builtin ownerless }');
		expect(await api.city('alpha')).toMatchObject({ id: 'alpha', status: 'DRAFT' });
		expect(calls[1].query).toContain('city(id: $id)');
		expect(calls[1].variables).toEqual({ id: 'alpha' });
	});

	it('fetches the raw config document', async () => {
		mockFetch({ cityConfig: '{"name":"Alpha","grid_size":5}' });
		expect(await api.cityConfig('alpha')).toBe('{"name":"Alpha","grid_size":5}');
		expect(calls[0].query).toContain('cityConfig(id: $id)');
		expect(calls[0].variables).toEqual({ id: 'alpha' });
	});

	it('sends city mutations with raw config', async () => {
		mockFetch({
			createCity: view,
			updateCity: view,
			publishCity: view,
			deleteCity: true,
			validateCity: { valid: true, publishable: false, issues: ['no depot'] }
		});
		await api.createCity('alpha', '{"name":"Alpha"}');
		await api.updateCity('alpha', '{"name":"Alpha 2"}');
		await api.publishCity('alpha');
		await api.deleteCity('alpha');
		expect(await api.validateCity('{"name":"Alpha"}')).toEqual({
			valid: true,
			publishable: false,
			issues: ['no depot']
		});
		expect(calls[0].query).toContain('createCity(id: $id, config: $config)');
		expect(calls[0].variables).toEqual({ id: 'alpha', config: '{"name":"Alpha"}' });
		expect(calls[1].query).toContain('updateCity(id: $id, config: $config)');
		expect(calls[2].query).toContain('publishCity(id: $id)');
		expect(calls[3].query).toContain('deleteCity(id: $id)');
		expect(calls[4].query).toContain('validateCity(config: $config)');
	});

	it('sends the stored admin key header', async () => {
		setAdminKey('sekret');
		mockFetch({ cities: [] });
		await api.cities();
		expect(calls[0].headers['X-Admin-Key']).toBe('sekret');
	});

	it('prefers extensions.code over the legacy message prefix', async () => {
		calls = [];
		vi.stubGlobal(
			'fetch',
			vi.fn(async (_url: string, init: RequestInit) => {
				calls.push({
					...JSON.parse(init.body as string),
					headers: (init.headers ?? {}) as Record<string, string>
				});
				return new Response(
					JSON.stringify({ errors: [{ message: 'no admin key', extensions: { code: 'FORBIDDEN' } }] }),
					{ status: 200 }
				);
			})
		);
		await expect(api.cities()).rejects.toMatchObject({ code: 'FORBIDDEN' });
		await expect(api.cities()).rejects.toBeInstanceOf(ApiError);
	});
});
