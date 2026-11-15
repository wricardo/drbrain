import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { readable } from 'svelte/store';
import SettingsPage from '../routes/settings/+page.svelte';
import ProfilePage from '../routes/u/[handle]/+page.svelte';
import { refreshAccount, setMe } from '$lib/stores/account';
import { rememberOwnerToken, ownerTokens } from '$lib/sessions';
import { PROFILE_QUERY, UPDATE_ACCOUNT_MUTATION, DELETE_ACCOUNT_MUTATION, CLAIM_SESSIONS_MUTATION, MAPS_QUERY } from '$lib/queries';
import type { Account, GameMap, Profile } from '$lib/types';

const { goto } = vi.hoisted(() => ({ goto: vi.fn() }));
vi.mock('$app/navigation', () => ({ goto }));
vi.mock('$app/stores', () => ({
	page: readable({ url: new URL('http://localhost/'), params: { handle: 'speedy_fox' } })
}));

const CLIENT_KEY = '$$_urql';
const me: Account = { handle: 'speedy_fox', displayName: 'Speedy', profilePublic: true, createdAt: '2026-01-01T00:00:00Z', handleChangeableAt: null };

const ok = (data: unknown) => ({ toPromise: () => Promise.resolve({ data }) });
const bad = (code: string, message: string) => ({
	toPromise: () => Promise.resolve({ error: { graphQLErrors: [{ message, extensions: { code } }] } })
});

type Handler = (doc: string, vars: Record<string, unknown>) => unknown;
function client(handler: Handler) {
	const call = vi.fn((doc: string, vars: Record<string, unknown>) => handler(doc, vars));
	return { client: { query: call, mutation: call } as never, call };
}
const ctx = (c: unknown) => new Map([[CLIENT_KEY, c]]);

async function signIn(account: Account | null) {
	await refreshAccount({ query: () => ok({ me: account, authProviders: [{ id: 'google', name: 'Google' }] }) } as never);
}

beforeEach(() => {
	localStorage.clear();
	goto.mockReset();
	setMe(null);
});
afterEach(cleanup);

describe('settings page', () => {
	it('asks a signed-out visitor to sign in, and says guests are fine', async () => {
		await signIn(null);
		render(SettingsPage, { context: ctx(client(() => ok({})).client) });
		const box = screen.getByTestId('settings-signed-out');
		expect(box).toHaveTextContent(/Guest play works without an account/);
		await fireEvent.click(within(box).getByRole('button', { name: 'Sign in' }));
		expect(screen.getByRole('dialog')).toBeInTheDocument();
	});

	it('shows the current profile and saves only what changed', async () => {
		await signIn(me);
		const { client: c, call } = client((doc, vars) => {
			expect(doc).toBe(UPDATE_ACCOUNT_MUTATION);
			void vars;
			return ok({ updateAccount: { ...me, handle: 'new_fox', profilePublic: false, handleChangeableAt: '2099-01-01T00:00:00Z' } });
		});
		render(SettingsPage, { context: ctx(c) });
		expect(screen.getByTestId('s-handle')).toHaveValue('speedy_fox');
		expect(screen.getByTestId('s-name')).toHaveValue('Speedy');
		expect(screen.getByTestId('vis-public')).toBeChecked();
		expect(screen.getByTestId('s-save')).toBeDisabled(); // nothing changed yet

		await fireEvent.input(screen.getByTestId('s-handle'), { target: { value: '  @New_Fox ' } });
		await fireEvent.click(screen.getByTestId('vis-private'));
		await fireEvent.click(screen.getByTestId('s-save'));
		await waitFor(() => expect(call).toHaveBeenCalled());
		expect(call.mock.calls[0][1]).toEqual({ input: { handle: 'new_fox', profilePublic: false } });
		await waitFor(() => expect(screen.getByText('Saved.')).toBeInTheDocument());
		await waitFor(() => expect(screen.getByTestId('handle-wait')).toBeInTheDocument());
	});

	it('blocks an impossible handle before asking the server', async () => {
		await signIn(me);
		const { client: c, call } = client(() => ok({}));
		render(SettingsPage, { context: ctx(c) });
		await fireEvent.input(screen.getByTestId('s-handle'), { target: { value: 'no spaces' } });
		expect(screen.getByRole('alert')).toHaveTextContent(/lower-case letters/);
		expect(screen.getByTestId('s-save')).toBeDisabled();
		expect(call).not.toHaveBeenCalled();
	});

	it('shows the server’s reason when a handle is taken', async () => {
		await signIn(me);
		const { client: c } = client(() => bad('INVALID_ARGUMENT', 'that handle is already taken'));
		render(SettingsPage, { context: ctx(c) });
		await fireEvent.input(screen.getByTestId('s-handle'), { target: { value: 'taken_one' } });
		await fireEvent.click(screen.getByTestId('s-save'));
		expect(await screen.findByTestId('s-problem')).toHaveTextContent('already taken');
	});

	it('adds guest runs with their owner tokens and forgets the ones it handled', async () => {
		await signIn(me);
		rememberOwnerToken('aaaaaaaaaaaaaaaa', 'tok-a');
		rememberOwnerToken('bbbbbbbbbbbbbbbb', 'tok-b');
		const { client: c, call } = client(() =>
			ok({ claimSessions: [{ sessionID: 'aaaaaaaaaaaaaaaa', status: 'ATTACHED' }, { sessionID: 'bbbbbbbbbbbbbbbb', status: 'REJECTED' }] })
		);
		render(SettingsPage, { context: ctx(c) });
		expect(screen.getByTestId('guest-offer')).toHaveTextContent('2 guest runs');
		await fireEvent.click(screen.getByTestId('s-claim'));
		expect(await screen.findByTestId('claim-result')).toHaveTextContent('Added 1 run to your profile. 1 could not be added');
		expect(call.mock.calls[0][0]).toBe(CLAIM_SESSIONS_MUTATION);
		expect(call.mock.calls[0][1]).toEqual({
			claims: [
				{ sessionID: 'aaaaaaaaaaaaaaaa', ownerToken: 'tok-a' },
				{ sessionID: 'bbbbbbbbbbbbbbbb', ownerToken: 'tok-b' }
			]
		});
		expect(ownerTokens()).toEqual({});
		expect(screen.queryByTestId('s-claim')).toBeNull();
	});

	describe('delete account', () => {
		it('needs an inline confirmation by typing the handle', async () => {
			await signIn(me);
			const { client: c, call } = client(() => ok({ deleteAccount: true }));
			const confirmSpy = vi.spyOn(window, 'confirm');
			render(SettingsPage, { context: ctx(c) });
			expect(screen.queryByTestId('delete-confirm')).toBeNull();
			await fireEvent.click(screen.getByTestId('delete-start'));
			const panel = screen.getByTestId('delete-confirm');
			expect(panel).toHaveTextContent('speedy_fox');
			const go = within(panel).getByTestId('delete-go');
			expect(go).toBeDisabled();
			await fireEvent.input(within(panel).getByTestId('delete-input'), { target: { value: 'speedy' } });
			expect(go).toBeDisabled();
			await fireEvent.input(within(panel).getByTestId('delete-input'), { target: { value: 'Speedy_Fox' } });
			expect(go).toBeEnabled();
			await fireEvent.click(go);
			await waitFor(() => expect(goto).toHaveBeenCalledWith('/'));
			expect(call.mock.calls[0][0]).toBe(DELETE_ACCOUNT_MUTATION);
			expect(call.mock.calls[0][1]).toEqual({ confirmHandle: 'speedy_fox' });
			expect(confirmSpy).not.toHaveBeenCalled(); // not window.confirm
		});

		it('forgets this browser’s owner tokens so old runs do not follow a new account', async () => {
			await signIn(me);
			rememberOwnerToken('aaaaaaaaaaaaaaaa', 'tok-a');
			const { client: c } = client(() => ok({ deleteAccount: true }));
			render(SettingsPage, { context: ctx(c) });
			await fireEvent.click(screen.getByTestId('delete-start'));
			await fireEvent.input(screen.getByTestId('delete-input'), { target: { value: 'speedy_fox' } });
			await fireEvent.click(screen.getByTestId('delete-go'));
			await waitFor(() => expect(goto).toHaveBeenCalled());
			expect(ownerTokens()).toEqual({});
		});

		it('can be cancelled and reports a server refusal', async () => {
			await signIn(me);
			const { client: c } = client(() => bad('INVALID_ARGUMENT', 'type your handle exactly to confirm'));
			render(SettingsPage, { context: ctx(c) });
			await fireEvent.click(screen.getByTestId('delete-start'));
			await fireEvent.input(screen.getByTestId('delete-input'), { target: { value: 'speedy_fox' } });
			await fireEvent.click(screen.getByTestId('delete-go'));
			expect(await screen.findByRole('alert')).toHaveTextContent('type your handle exactly');
			expect(goto).not.toHaveBeenCalled();
			await fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
			expect(screen.queryByTestId('delete-confirm')).toBeNull();
		});
	});
});

const map = (id: string, name: string, builtin: boolean, difficulty = 'easy') => ({ id, name, difficulty, builtin }) as GameMap;
const maps = [map('e1', 'Level 1', true), map('e2', 'Level 2', true), map('c1', 'Community 1', false)];
const entry = (rank: number, session: string, at: string) => ({
	rank, sessionId: session, displayName: 'Speedy', handle: 'speedy_fox', mine: false, slots: 5, steps: 9, achievedAt: at
});
const profile = (over: Partial<Profile> = {}): Profile => ({
	handle: 'speedy_fox',
	displayName: 'Speedy',
	createdAt: '2026-01-15T00:00:00Z',
	isMine: false,
	runs: [
		{ mapId: 'e1', total: 7, entry: entry(1, 'aaaaaaaaaaaaaaaa', '2026-02-01T00:00:00Z') },
		{ mapId: 'c1', total: 2, entry: entry(2, 'bbbbbbbbbbbbbbbb', '2026-03-01T00:00:00Z') }
	],
	...over
});

describe('profile page', () => {
	const load = (p: Profile | null) =>
		client((doc, vars) => {
			if (doc === MAPS_QUERY) return ok({ maps });
			expect(doc).toBe(PROFILE_QUERY);
			expect(vars).toEqual({ handle: 'speedy_fox' });
			return ok({ profile: p });
		});

	it('shows the handle, the official and community tallies and the best run per map', async () => {
		await signIn(null);
		render(ProfilePage, { context: ctx(load(profile()).client) });
		expect(await screen.findByTestId('profile-name')).toHaveTextContent('Speedy');
		expect(screen.getByTestId('profile-handle')).toHaveTextContent('@speedy_fox');
		expect(screen.getByTestId('tally-official')).toHaveTextContent('1 of 2 solved');
		expect(screen.getByTestId('tally-community')).toHaveTextContent('1 solved of 1');
		const rows = within(screen.getByTestId('profile-table')).getAllByRole('row').slice(1);
		expect(rows).toHaveLength(2);
		expect(rows[0]).toHaveTextContent('Level 1'); // standard map order: official maps first, then community
		expect(rows[1]).toHaveTextContent('Community 1');
		expect(within(rows[0]).getByRole('link', { name: 'Watch' })).toHaveAttribute('href', '/watch/aaaaaaaaaaaaaaaa');
		expect(within(rows[0]).getByRole('link', { name: 'Leaderboard' })).toHaveAttribute('href', '/leaderboard?map=e1');
		expect(within(screen.getByTestId('profile-recent')).getAllByRole('listitem')[0]).toHaveTextContent('Community 1');
		expect(screen.queryByTestId('profile-mine')).toBeNull();
	});

	it('says "no profile" for an unknown or private handle', async () => {
		await signIn(null);
		render(ProfilePage, { context: ctx(load(null).client) });
		expect(await screen.findByTestId('profile-missing')).toHaveTextContent('@speedy_fox');
		expect(screen.queryByTestId('profile-table')).toBeNull();
	});

	it('tells the owner when the profile is private', async () => {
		await signIn({ ...me, profilePublic: false });
		render(ProfilePage, { context: ctx(load(profile({ isMine: true })).client) });
		expect(await screen.findByTestId('profile-mine')).toHaveTextContent('only you can see this page');
	});

	it('shows an empty state for a player with no wins', async () => {
		await signIn(null);
		render(ProfilePage, { context: ctx(load(profile({ runs: [] })).client) });
		expect(await screen.findByTestId('profile-empty')).toBeInTheDocument();
		expect(screen.getByTestId('tally-official')).toHaveTextContent('0 of 2 solved');
	});
});
