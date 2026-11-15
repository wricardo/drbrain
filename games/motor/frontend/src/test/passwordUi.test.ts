import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { readable } from 'svelte/store';
import PasswordForm from '$lib/components/PasswordForm.svelte';
import SignInDialog from '$lib/components/SignInDialog.svelte';
import SettingsPage from '../routes/settings/+page.svelte';
import { passwordProblem, submitPassword } from '$lib/account';
import { refreshAccount, setMe } from '$lib/stores/account';
import { CHANGE_PASSWORD_MUTATION } from '$lib/queries';
import type { Account } from '$lib/types';

const { goto } = vi.hoisted(() => ({ goto: vi.fn() }));
vi.mock('$app/navigation', () => ({ goto }));
vi.mock('$app/stores', () => ({ page: readable({ url: new URL('http://localhost/'), params: {} }) }));

const CLIENT_KEY = '$$_urql';
const pwAccount: Account = {
	handle: 'speedy_fox',
	displayName: 'Speedy',
	profilePublic: true,
	createdAt: '2026-01-01T00:00:00Z',
	handleChangeableAt: null,
	hasPassword: true
};
const ok = (data: unknown) => ({ toPromise: () => Promise.resolve({ data }) });
const bad = (code: string, message: string) => ({
	toPromise: () => Promise.resolve({ error: { graphQLErrors: [{ message, extensions: { code } }] } })
});

const jsonResponse = (status: number, body: unknown) => new Response(JSON.stringify(body), { status });

beforeEach(() => {
	setMe(null);
});
afterEach(() => {
	cleanup();
	vi.unstubAllGlobals();
});

describe('passwordProblem', () => {
	it.each([
		['short', 'at least 8'],
		['ñññññññ', 'at least 8'],
		['a'.repeat(129), 'too long'],
		['Speedy_Fox', 'same as your username']
	])('rejects %j', (pw, text) => {
		expect(passwordProblem(pw, 'speedy_fox')).toContain(text);
	});
	it.each(['tangerine-ladder-42', 'a b c d e f g', 'ñandúñandú', 'a'.repeat(128)])('accepts %j', (pw) => {
		expect(passwordProblem(pw, 'speedy_fox')).toBeNull();
	});
});

describe('submitPassword', () => {
	it('posts same-origin JSON with a normalised username', async () => {
		const doFetch = vi.fn(async () => jsonResponse(200, { handle: 'speedy_fox' }));
		const r = await submitPassword('register', '  @Speedy_Fox ', 'tangerine-ladder-42', doFetch as never);
		expect(r).toEqual({ ok: true, message: '' });
		const [url, init] = doFetch.mock.calls[0] as unknown as [string, RequestInit];
		expect(url).toBe('/auth/password/register');
		expect(init.method).toBe('POST');
		expect((init.headers as Record<string, string>)['Content-Type']).toBe('application/json');
		expect(JSON.parse(init.body as string)).toEqual({ handle: 'speedy_fox', password: 'tangerine-ladder-42' });
	});
	it("shows the server's reason, a rate-limit text and network failures", async () => {
		expect(await submitPassword('login', 'a', 'b', (async () => jsonResponse(401, { error: 'wrong username or password' })) as never)).toEqual({
			ok: false,
			message: 'wrong username or password'
		});
		expect((await submitPassword('login', 'a', 'b', (async () => new Response('', { status: 429 })) as never)).message).toContain('Too many');
		expect((await submitPassword('login', 'a', 'b', (async () => new Response('<html>', { status: 502 })) as never)).message).toContain('did not work');
		expect((await submitPassword('login', 'a', 'b', (async () => Promise.reject(new Error('offline'))) as never)).message).toContain('Could not reach');
	});
});

describe('PasswordForm', () => {
	it('signs in and then goes to the validated return path', async () => {
		const doFetch = vi.fn(async (_url: string, _init?: RequestInit) => jsonResponse(200, { handle: 'speedy_fox' }));
		vi.stubGlobal('fetch', doFetch);
		const onsuccess = vi.fn();
		render(PasswordForm, { props: { next: '/play/abc', onsuccess } });
		const submit = screen.getByTestId('pw-submit');
		expect(submit).toBeDisabled();
		await fireEvent.input(screen.getByTestId('pw-handle'), { target: { value: 'Speedy_Fox' } });
		await fireEvent.input(screen.getByTestId('pw-password'), { target: { value: 'tangerine-ladder-42' } });
		await fireEvent.click(submit);
		await waitFor(() => expect(onsuccess).toHaveBeenCalledWith('/play/abc'));
		expect(doFetch.mock.calls[0][0]).toBe('/auth/password/login');
	});

	it('never follows a hostile return path', async () => {
		vi.stubGlobal('fetch', vi.fn(async () => jsonResponse(200, {})));
		const onsuccess = vi.fn();
		render(PasswordForm, { props: { next: 'https://evil.example', onsuccess } });
		await fireEvent.input(screen.getByTestId('pw-handle'), { target: { value: 'a_user' } });
		await fireEvent.input(screen.getByTestId('pw-password'), { target: { value: 'tangerine-ladder-42' } });
		await fireEvent.click(screen.getByTestId('pw-submit'));
		await waitFor(() => expect(onsuccess).toHaveBeenCalledWith('/'));
	});

	it('shows a failed sign-in without leaving the page', async () => {
		vi.stubGlobal('fetch', vi.fn(async () => jsonResponse(401, { error: 'wrong username or password' })));
		const onsuccess = vi.fn();
		render(PasswordForm, { props: { next: '/', onsuccess } });
		await fireEvent.input(screen.getByTestId('pw-handle'), { target: { value: 'speedy_fox' } });
		await fireEvent.input(screen.getByTestId('pw-password'), { target: { value: 'nope-nope-nope' } });
		await fireEvent.click(screen.getByTestId('pw-submit'));
		expect(await screen.findByTestId('pw-error')).toHaveTextContent('wrong username or password');
		expect(onsuccess).not.toHaveBeenCalled();
	});

	it('warns that there is no reset before an account is created, and checks the obvious problems', async () => {
		const doFetch = vi.fn(async (_url: string, _init?: RequestInit) => jsonResponse(200, {}));
		vi.stubGlobal('fetch', doFetch);
		render(PasswordForm, { props: { next: '/', onsuccess: () => {} } });
		expect(screen.queryByTestId('pw-warning')).toBeNull();
		await fireEvent.click(screen.getByTestId('pw-tab-register'));
		expect(screen.getByTestId('pw-warning')).toHaveTextContent(/No email, no reset/);
		expect(screen.getByTestId('pw-submit')).toHaveTextContent('Create account');
		expect(screen.getByTestId('pw-password')).toHaveAttribute('autocomplete', 'new-password');

		await fireEvent.input(screen.getByTestId('pw-handle'), { target: { value: 'no spaces' } });
		await fireEvent.input(screen.getByTestId('pw-password'), { target: { value: 'short' } });
		expect(screen.getAllByRole('alert').map((a) => a.textContent).join(' ')).toMatch(/lower-case letters.*at least 8/);
		expect(screen.getByTestId('pw-submit')).toBeDisabled();
		expect(doFetch).not.toHaveBeenCalled();

		await fireEvent.input(screen.getByTestId('pw-handle'), { target: { value: 'speedy_fox' } });
		await fireEvent.input(screen.getByTestId('pw-password'), { target: { value: 'tangerine-ladder-42' } });
		expect(screen.getByTestId('pw-submit')).toBeEnabled();
		await fireEvent.click(screen.getByTestId('pw-submit'));
		await waitFor(() => expect(doFetch).toHaveBeenCalled());
		expect(doFetch.mock.calls[0][0]).toBe('/auth/password/register');
	});

	it('can show the password', async () => {
		render(PasswordForm, { props: { next: '/' } });
		const input = screen.getByTestId('pw-password');
		expect(input).toHaveAttribute('type', 'password');
		await fireEvent.click(screen.getByRole('checkbox', { name: 'Show password' }));
		expect(input).toHaveAttribute('type', 'text');
	});
});

describe('SignInDialog with the password option', () => {
	const google = { id: 'google', name: 'Google' };
	const password = { id: 'password', name: 'Username and password' };

	it('shows Google/GitHub as links and the form below them', () => {
		render(SignInDialog, { props: { providers: [google, password], next: '/', onclose: () => {} } });
		expect(screen.getByTestId('signin-google')).toHaveAttribute('href', '/auth/google/login?next=%2F');
		expect(screen.queryByTestId('signin-password')).toBeNull(); // the password option is a form, never a link
		expect(screen.getByTestId('password-form')).toBeInTheDocument();
	});

	it('works with the password option alone', () => {
		render(SignInDialog, { props: { providers: [password], next: '/', onclose: () => {} } });
		expect(screen.queryByRole('link')).toBeNull();
		expect(screen.getByTestId('password-form')).toBeInTheDocument();
	});

	it('shows no form when password sign-in is off', () => {
		render(SignInDialog, { props: { providers: [google], next: '/', onclose: () => {} } });
		expect(screen.queryByTestId('password-form')).toBeNull();
	});
});

describe('settings: change password', () => {
	async function signIn(a: Account) {
		await refreshAccount({ query: () => ok({ me: a, authProviders: [] }) } as never);
	}

	it('is offered only to password accounts', async () => {
		await signIn({ ...pwAccount, hasPassword: false });
		render(SettingsPage, { context: new Map([[CLIENT_KEY, { query: vi.fn(), mutation: vi.fn() }]]) });
		expect(screen.queryByTestId('password-settings')).toBeNull();
	});

	it('changes the password with the current one and says the other browsers were signed out', async () => {
		await signIn(pwAccount);
		const mutation = vi.fn(() => ok({ changePassword: true }));
		render(SettingsPage, { context: new Map([[CLIENT_KEY, { query: vi.fn(), mutation }]]) });
		const form = screen.getByTestId('password-settings');
		expect(within(form).getByTestId('s-pw-save')).toBeDisabled();
		await fireEvent.input(within(form).getByTestId('s-cur-pw'), { target: { value: 'tangerine-ladder-42' } });
		await fireEvent.input(within(form).getByTestId('s-new-pw'), { target: { value: 'short' } });
		expect(within(form).getByRole('alert')).toHaveTextContent('at least 8');
		expect(within(form).getByTestId('s-pw-save')).toBeDisabled();
		await fireEvent.input(within(form).getByTestId('s-new-pw'), { target: { value: 'brand-new-pass-77' } });
		await fireEvent.click(within(form).getByTestId('s-pw-save'));
		expect(await within(form).findByTestId('s-pw-ok')).toHaveTextContent('other browsers were signed out');
		expect(mutation).toHaveBeenCalledWith(CHANGE_PASSWORD_MUTATION, { currentPassword: 'tangerine-ladder-42', newPassword: 'brand-new-pass-77' });
		expect(within(form).getByTestId('s-cur-pw')).toHaveValue(''); // nothing lingers in the form
		expect(within(form).getByTestId('s-new-pw')).toHaveValue('');
	});

	it("shows the server's reason for a wrong current password", async () => {
		await signIn(pwAccount);
		const mutation = vi.fn(() => bad('INVALID_ARGUMENT', 'the current password is wrong'));
		render(SettingsPage, { context: new Map([[CLIENT_KEY, { query: vi.fn(), mutation }]]) });
		await fireEvent.input(screen.getByTestId('s-cur-pw'), { target: { value: 'not-my-password' } });
		await fireEvent.input(screen.getByTestId('s-new-pw'), { target: { value: 'brand-new-pass-77' } });
		await fireEvent.click(screen.getByTestId('s-pw-save'));
		expect(await screen.findByTestId('s-pw-error')).toHaveTextContent('the current password is wrong');
	});
});
