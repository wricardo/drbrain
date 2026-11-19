import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { readable } from 'svelte/store';
import type { Client } from '@urql/svelte';
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
/** What a stubbed urql operation answers with; `unwrap` only awaits `toPromise`. */
interface OperationAnswer<T> {
	toPromise(): Promise<T>;
}

/** A GraphQL result carrying data. */
const ok = <T>(data: T): OperationAnswer<{ data: T }> => ({ toPromise: () => Promise.resolve({ data }) });

/** A GraphQL error result. */
const bad = (code: string, message: string): OperationAnswer<unknown> => ({
	toPromise: () => Promise.resolve({ error: { graphQLErrors: [{ message, extensions: { code } }] } })
});

/** Records the document and variables a mutation was called with. */
interface MutationMock {
	(...args: [doc: string, vars?: Record<string, unknown>]): OperationAnswer<unknown>;
	mock: { calls: [string, Record<string, unknown> | undefined][] };
}

/** A mutation that always answers with `answer`, recording how it was called. */
function mutationMock(answer: OperationAnswer<unknown>): MutationMock {
	return vi.fn((_doc: string, _vars?: Record<string, unknown>) => answer) as unknown as MutationMock;
}

/**
 * The part of the urql client `submitPassword` takes, named so tests do not
 * restate `Pick<Client, 'mutation'>`.
 */
type PasswordClient = Pick<Client, 'mutation'>;

/**
 * A client whose `mutation` answers with `answer`. The cast is unchecked and
 * deliberately narrow: a real urql mutation returns an operation object, while
 * these tests only need the promise `unwrap` awaits.
 */
function clientAnswering(mutation: MutationMock): PasswordClient {
	return { mutation } as unknown as PasswordClient;
}

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
	it('sends the mutation with a normalised username', async () => {
		const mutation = mutationMock(ok({ registerWithPassword: pwAccount }));
		const r = await submitPassword('register', '  @Speedy_Fox ', 'tangerine-ladder-42', clientAnswering(mutation));
		expect(r).toEqual({ ok: true, message: '' });
		const [doc, vars] = mutation.mock.calls[0];
		expect(doc).toContain('registerWithPassword');
		expect(vars).toEqual({ handle: 'speedy_fox', password: 'tangerine-ladder-42' });
	});

	it('signs in with the other mutation', async () => {
		const mutation = mutationMock(ok({ logInWithPassword: pwAccount }));
		await submitPassword('login', 'speedy_fox', 'tangerine-ladder-42', clientAnswering(mutation));
		expect(mutation.mock.calls[0][0]).toContain('logInWithPassword');
	});

	it("shows the server's reason, a lockout and a network failure", async () => {
		const wrong = await submitPassword(
			'login',
			'a',
			'b',
			clientAnswering(mutationMock(bad('UNAUTHENTICATED', 'wrong username or password')))
		);
		expect(wrong).toEqual({ ok: false, message: 'wrong username or password' });

		const locked = await submitPassword(
			'login',
			'a',
			'b',
			clientAnswering(mutationMock(bad('LIMIT_REACHED', 'too many failed attempts; try again in 15 minutes')))
		);
		expect(locked.message).toContain('too many failed attempts');

		const offline = await submitPassword(
			'login',
			'a',
			'b',
			clientAnswering(mutationMock({ toPromise: () => Promise.reject(new Error('offline')) }))
		);
		expect(offline.ok).toBe(false);
		expect(offline.message).toBeTruthy();
	});
});

describe('PasswordForm', () => {
	/** Renders the form with a client whose mutations answer `answer`. */
	function renderForm(next: string, answer: OperationAnswer<unknown>) {
		const mutation = mutationMock(answer);
		const onsuccess = vi.fn();
		render(PasswordForm, { props: { next, onsuccess }, context: new Map([[CLIENT_KEY, { query: vi.fn(), mutation }]]) });
		return { mutation, onsuccess };
	}

	it('signs in and then goes to the validated return path', async () => {
		const { mutation, onsuccess } = renderForm('/play/abc', ok({ logInWithPassword: pwAccount }));
		const submit = screen.getByTestId('pw-submit');
		expect(submit).toBeDisabled();
		await fireEvent.input(screen.getByTestId('pw-handle'), { target: { value: 'Speedy_Fox' } });
		await fireEvent.input(screen.getByTestId('pw-password'), { target: { value: 'tangerine-ladder-42' } });
		await fireEvent.click(submit);
		await waitFor(() => expect(onsuccess).toHaveBeenCalledWith('/play/abc'));
		expect(mutation.mock.calls[0][0]).toContain('logInWithPassword');
	});

	it('never follows a hostile return path', async () => {
		const { onsuccess } = renderForm('https://evil.example', ok({ logInWithPassword: pwAccount }));
		await fireEvent.input(screen.getByTestId('pw-handle'), { target: { value: 'a_user' } });
		await fireEvent.input(screen.getByTestId('pw-password'), { target: { value: 'tangerine-ladder-42' } });
		await fireEvent.click(screen.getByTestId('pw-submit'));
		await waitFor(() => expect(onsuccess).toHaveBeenCalledWith('/'));
	});

	it('shows a failed sign-in without leaving the page', async () => {
		const { onsuccess } = renderForm('/', bad('UNAUTHENTICATED', 'wrong username or password'));
		await fireEvent.input(screen.getByTestId('pw-handle'), { target: { value: 'speedy_fox' } });
		await fireEvent.input(screen.getByTestId('pw-password'), { target: { value: 'nope-nope-nope' } });
		await fireEvent.click(screen.getByTestId('pw-submit'));
		expect(await screen.findByTestId('pw-error')).toHaveTextContent('wrong username or password');
		expect(onsuccess).not.toHaveBeenCalled();
	});

	it('warns that there is no reset before an account is created, and checks the obvious problems', async () => {
		const { mutation } = renderForm('/', ok({ registerWithPassword: pwAccount }));
		expect(screen.queryByTestId('pw-warning')).toBeNull();
		await fireEvent.click(screen.getByTestId('pw-tab-register'));
		expect(screen.getByTestId('pw-warning')).toHaveTextContent(/No email, no reset/);
		expect(screen.getByTestId('pw-submit')).toHaveTextContent('Create account');
		expect(screen.getByTestId('pw-password')).toHaveAttribute('autocomplete', 'new-password');

		await fireEvent.input(screen.getByTestId('pw-handle'), { target: { value: 'no spaces' } });
		await fireEvent.input(screen.getByTestId('pw-password'), { target: { value: 'short' } });
		expect(screen.getAllByRole('alert').map((a) => a.textContent).join(' ')).toMatch(/lower-case letters.*at least 8/);
		expect(screen.getByTestId('pw-submit')).toBeDisabled();
		expect(mutation).not.toHaveBeenCalled();

		await fireEvent.input(screen.getByTestId('pw-handle'), { target: { value: 'speedy_fox' } });
		await fireEvent.input(screen.getByTestId('pw-password'), { target: { value: 'tangerine-ladder-42' } });
		expect(screen.getByTestId('pw-submit')).toBeEnabled();
		await fireEvent.click(screen.getByTestId('pw-submit'));
		await waitFor(() => expect(mutation).toHaveBeenCalled());
		expect(mutation.mock.calls[0][0]).toContain('registerWithPassword');
	});

	it('can show the password', async () => {
		renderForm('/', ok({ logInWithPassword: pwAccount }));
		const input = screen.getByTestId('pw-password');
		expect(input).toHaveAttribute('type', 'password');
		await fireEvent.click(screen.getByRole('checkbox', { name: 'Show password' }));
		expect(input).toHaveAttribute('type', 'text');
	});
});

describe('SignInDialog with the password option', () => {
	const google = { id: 'google', name: 'Google' };
	const password = { id: 'password', name: 'Username and password' };
	/** The dialog hosts PasswordForm, which needs a urql client in context. */
	const context = new Map<string, unknown>([[CLIENT_KEY, { query: vi.fn(), mutation: vi.fn(() => ok({ logInWithPassword: pwAccount })) }]]);

	it('shows Google/GitHub as links and the form below them', () => {
		render(SignInDialog, { props: { providers: [google, password], next: '/', onclose: () => {} }, context });
		expect(screen.getByTestId('signin-google')).toHaveAttribute('href', '/auth/google/login?next=%2F');
		expect(screen.queryByTestId('signin-password')).toBeNull(); // the password option is a form, never a link
		expect(screen.getByTestId('password-form')).toBeInTheDocument();
	});

	it('works with the password option alone', () => {
		render(SignInDialog, { props: { providers: [password], next: '/', onclose: () => {} }, context });
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
