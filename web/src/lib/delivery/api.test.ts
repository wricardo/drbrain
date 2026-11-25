import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { api, ApiError } from './api';
import { setAdminKey } from './admin';
import { ownerTokenFor, ownerTokens, rememberOwnerToken } from './ownerTokens';

type Call = { query: string; variables: Record<string, unknown>; headers: Record<string, string> };
let calls: Call[];

/** Replaces fetch with one that records the operation and answers { data: { delivery } }, or { data } for flat root mutations. */
function mockFetch(delivery: unknown, root = false) {
	calls = [];
	vi.stubGlobal(
		'fetch',
		vi.fn(async (_url: string, init: RequestInit) => {
			calls.push({
				...JSON.parse(init.body as string),
				headers: (init.headers ?? {}) as Record<string, string>
			});
			return new Response(JSON.stringify({ data: root ? delivery : { delivery } }), { status: 200 });
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
		mockFetch({ deliveryCreateSession: { session: { id: 's1', cars: [] }, ownerToken: 'tok-1' } }, true);
		const snap = await api.createSession(undefined, undefined, 'alpha');
		expect(snap.id).toBe('s1');
		expect(calls[0].query).toContain('deliveryCreateSession(id: $id, seed: $seed, cityId: $cityId)');
		expect(calls[0].query).toContain('ownerToken');
		expect(calls[0].variables).toMatchObject({ cityId: 'alpha' });
		expect(ownerTokenFor('s1')).toBe('tok-1');
	});

	it('a gated mutation sends the stored ownerToken', async () => {
		rememberOwnerToken('s1', 'tok-1');
		mockFetch({ deliveryClearMoves: { id: 'car_1' } }, true);
		await api.clearMoves('s1', 'car_1');
		expect(calls[0].query).toContain('$ownerToken: String');
		expect(calls[0].query).toContain('deliveryClearMoves(id: $id, ownerToken: $ownerToken');
		expect(calls[0].variables).toMatchObject({ id: 's1', ownerToken: 'tok-1', carId: 'car_1' });
	});

	it('a gated mutation sends null for a session without a token', async () => {
		mockFetch({ deliverySetSpeed: { id: 'legacy' } }, true);
		await api.setSpeed('legacy', 1);
		expect(calls[0].variables.ownerToken).toBeNull();
	});

	it('sessions selects the owner fields', async () => {
		mockFetch({ sessions: [] });
		await api.sessions();
		expect(calls[0].query).toContain('ownerDisplayName ownerHandle mine');
		expect(calls[0].variables).toEqual({ sort: null, limit: null, mine: null });
	});

	it('sessions passes the sort, limit and mine filter', async () => {
		mockFetch({ sessions: [] });
		await api.sessions({ sort: 'TICK', limit: 25, mine: true });
		expect(calls[0].variables).toEqual({ sort: 'TICK', limit: 25, mine: true });
	});

	it('deleteSession forgets the token', async () => {
		rememberOwnerToken('s1', 'tok-1');
		rememberOwnerToken('s2', 'tok-2');
		mockFetch({ deliveryDeleteSession: true }, true);
		await api.deleteSession('s1');
		expect(calls[0].variables.ownerToken).toBe('tok-1');
		expect(ownerTokens()).toEqual({ s2: 'tok-2' });
	});

	it('deleteSession keeps the token when the server says no', async () => {
		rememberOwnerToken('s1', 'tok-1');
		mockFetch({ deliveryDeleteSession: false }, true);
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
		mockFetch({ deliveryClaimSessions: results }, true);
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
		mockFetch(
			{
				deliveryCreateCity: view,
				deliveryUpdateCity: view,
				deliveryPublishCity: view,
				deliveryDeleteCity: true,
				deliveryValidateCity: { valid: true, publishable: false, issues: ['no depot'] }
			},
			true
		);
		await api.createCity('alpha', '{"name":"Alpha"}');
		await api.updateCity('alpha', '{"name":"Alpha 2"}');
		await api.publishCity('alpha');
		await api.deleteCity('alpha');
		expect(await api.validateCity('{"name":"Alpha"}')).toEqual({
			valid: true,
			publishable: false,
			issues: ['no depot']
		});
		expect(calls[0].query).toContain('deliveryCreateCity(id: $id, config: $config)');
		expect(calls[0].variables).toEqual({ id: 'alpha', config: '{"name":"Alpha"}' });
		expect(calls[1].query).toContain('deliveryUpdateCity(id: $id, config: $config)');
		expect(calls[2].query).toContain('deliveryPublishCity(id: $id)');
		expect(calls[3].query).toContain('deliveryDeleteCity(id: $id)');
		expect(calls[4].query).toContain('deliveryValidateCity(config: $config)');
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

describe('delivery analytics metrics', () => {
	it('fetches every cumulative metric in one aliased request', async () => {
		mockFetch({
			money: [{ sessionId: 's1', cityId: 'city', points: [{ tick: 0, value: 50 }] }],
			boxesDelivered: [],
			reputation: [],
			deliveries: [{ sessionId: 's1', cityId: 'city', points: [{ tick: 0, value: 0 }] }],
			lateDeliveries: [],
			expiredOrders: [],
			ordersCreated: [],
			deliveryIncome: [],
			orderFines: [],
			upkeepSpend: [],
			chargingSpend: [],
			towSpend: []
		});
		const out = await api.metricsHistory(['s1'], { maxPoints: 200 });
		expect(calls).toHaveLength(1);
		expect(calls[0].query).toContain('metricHistory(metric: MONEY');
		expect(calls[0].query).toContain('metricHistory(metric: BOXES_DELIVERED');
		expect(calls[0].query).toContain('metricHistory(metric: DELIVERIES');
		expect(calls[0].query).toContain('metricHistory(metric: TOW_SPEND');
		expect(calls[0].variables).toMatchObject({ ids: ['s1'], maxPoints: 200 });
		expect(out.MONEY[0].points[0].value).toBe(50);
		expect(out.DELIVERIES[0].sessionId).toBe('s1');
		expect(out.UPKEEP_SPEND).toEqual([]);
	});
});
