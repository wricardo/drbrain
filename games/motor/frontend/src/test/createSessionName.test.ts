import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from 'vitest';
import HomePage from '../routes/+page.svelte';
import MapsPage from '../routes/maps/+page.svelte';
import { refreshAccount, setMe } from '$lib/stores/account';
import { saveDisplayName } from '$lib/sessions';
import { CREATE_SESSION_MUTATION, MAPS_QUERY, SESSIONS_QUERY } from '$lib/queries';
import type { Account, GameMap } from '$lib/types';

const { goto } = vi.hoisted(() => ({ goto: vi.fn() }));
vi.mock('$app/navigation', () => ({ goto }));

const CLIENT_KEY = '$$_urql';
const me: Account = {
	handle: 'speedy_fox',
	displayName: 'Speedy',
	profilePublic: true,
	createdAt: '2026-01-01T00:00:00Z',
	handleChangeableAt: null,
	hasPassword: false
};

const map = (id: string, name: string): GameMap =>
	({
		id,
		name,
		description: 'A test map',
		difficulty: 'easy',
		builtin: true,
		width: 4,
		height: 1,
		layout: ['>..*'],
		start: { x: 0, y: 0 },
		startFacing: 'RIGHT',
		rocks: [],
		treats: [{ x: 3, y: 0 }],
		conveyors: [],
		mirrors: [],
		keys: [],
		doors: [],
		portals: [],
		mainTapeLength: 8,
		subTapeLengths: [],
		maxSteps: 120,
		maxCallDepth: 16,
		allowRecursion: false
	}) as GameMap;

const maps = [map('wide', 'A Wide Open'), map('tall', 'Z Tall Order')];
const created = { id: 's1', displayName: me.displayName, ownerToken: 'tok', vm: { status: 'READY', steps: 0 } };

/** A urql result the mocked client can return. */
const ok = (data: unknown) => ({ toPromise: () => Promise.resolve({ data }) });
const bad = (message: string) => ({
	toPromise: () => Promise.resolve({ error: { graphQLErrors: [{ message, extensions: { code: 'INTERNAL' } }] } })
});

type GqlCall = Mock<(doc: string, vars: Record<string, unknown>) => unknown>;
interface FakeClient {
	client: never;
	/** Every query and mutation the page sent, in order. */
	call: GqlCall;
}

/** A urql client that answers the page's queries and records every mutation's variables. */
function client(fail: boolean): FakeClient {
	const call = vi.fn((doc: string, vars: Record<string, unknown>) => {
		if (doc === MAPS_QUERY) return ok({ maps });
		if (doc === SESSIONS_QUERY) return ok({ sessions: [] });
		if (doc === CREATE_SESSION_MUTATION) return fail ? bad('boom') : ok({ createSession: created });
		throw new Error(`unexpected document: ${doc}`);
	});
	return { client: { query: call, mutation: call } as never, call };
}

const ctx = (c: unknown) => new Map([[CLIENT_KEY, c]]);

async function signIn(account: Account | null) {
	await refreshAccount({ query: () => ok({ me: account, authProviders: [] }) } as never);
}

function createVars(call: GqlCall): Record<string, unknown> {
	const found = call.mock.calls.find(([doc]) => doc === CREATE_SESSION_MUTATION);
	if (!found) throw new Error('createSession was never called');
	return found[1];
}

/** The home page's start button only acts once the map list has loaded. */
async function startFirstMap(): Promise<void> {
	const button = await screen.findByTestId('play-easiest');
	await waitFor(() => expect(button).toBeEnabled());
	await fireEvent.click(button);
}

/** Clicks Play on the named card of the maps page. */
async function playMap(name: string): Promise<void> {
	const card = (await screen.findAllByTestId('map-card')).find((c) => within(c).queryByText(name));
	if (!card) throw new Error(`no map card for ${name}`);
	await fireEvent.click(within(card).getByRole('button', { name: 'Play' }));
}

beforeEach(() => {
	localStorage.clear();
	goto.mockReset();
	setMe(null);
});
afterEach(cleanup);

describe('creating a session signed in', () => {
	it('sends no name, so the server names the run after the account', async () => {
		await signIn(me);
		saveDisplayName('BraveOtter'); // an alias this browser kept from before signing in
		const { client: c, call } = client(false);
		render(HomePage, { context: ctx(c) });
		await startFirstMap();
		await waitFor(() => expect(call).toHaveBeenCalled());
		expect(createVars(call)).toEqual({ mapID: 'wide', displayName: null });
		expect(goto).toHaveBeenCalledWith('/play/s1');
	});

	it('sends no name from the maps page either', async () => {
		await signIn(me);
		saveDisplayName('BraveOtter');
		const { client: c, call } = client(false);
		render(MapsPage, { context: ctx(c) });
		await playMap('A Wide Open');
		await waitFor(() => expect(call).toHaveBeenCalled());
		expect(createVars(call)).toEqual({ mapID: 'wide', displayName: null });
	});
});

describe('creating a session as a guest', () => {
	it('sends the name typed on the home page', async () => {
		await signIn(null);
		saveDisplayName('Ada');
		const { client: c, call } = client(false);
		render(HomePage, { context: ctx(c) });
		await startFirstMap();
		await waitFor(() => expect(call).toHaveBeenCalled());
		expect(createVars(call)).toEqual({ mapID: 'wide', displayName: 'Ada' });
	});

	it('sends none when the player never picked one, and keeps the random guest name the server picks', async () => {
		await signIn(null);
		const { client: c, call } = client(false);
		render(MapsPage, { context: ctx(c) });
		await playMap('A Wide Open');
		await waitFor(() => expect(call).toHaveBeenCalled());
		expect(createVars(call)).toEqual({ mapID: 'wide', displayName: null });
	});

	it('reports a failed create instead of navigating', async () => {
		await signIn(null);
		const { client: c } = client(true);
		render(HomePage, { context: ctx(c) });
		await startFirstMap();
		expect(await screen.findByRole('alert')).toBeInTheDocument();
		expect(goto).not.toHaveBeenCalled();
	});
});
