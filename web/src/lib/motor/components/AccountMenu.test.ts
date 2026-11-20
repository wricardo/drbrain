import { cleanup, fireEvent, render, screen, within } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { readable } from 'svelte/store';
import AccountMenu from './AccountMenu.svelte';
import SignInDialog from '$lib/account/components/SignInDialog.svelte';
import { setMe, refreshAccount } from '$lib/account/stores/account';
import type { Account } from '$lib/account/types';

vi.mock('$app/stores', () => ({
	page: readable({ url: new URL('http://localhost/leaderboard?map=a') })
}));

const CLIENT_KEY = '$$_urql';
const me: Account = { handle: 'speedy_fox', displayName: 'Speedy', profilePublic: true, createdAt: '2026-01-01T00:00:00Z', handleChangeableAt: null, hasPassword: false };

// The store is module-level, so each test sets the state through refreshAccount with a fake client.
async function load(data: { me: Account | null; authProviders: { id: string; name: string }[] }) {
	await refreshAccount({ query: () => ({ toPromise: () => Promise.resolve({ data: { account: data } }) }) } as never);
}

function mount(mutation = vi.fn(() => ({ toPromise: () => Promise.resolve({ data: { logout: true } }) }))) {
	render(AccountMenu, { context: new Map([[CLIENT_KEY, { mutation } as never]]) });
	return { mutation };
}

beforeEach(() => setMe(null));
afterEach(cleanup);

describe('AccountMenu', () => {
	it('shows nothing when the server offers no sign-in (the feature is off)', async () => {
		await load({ me: null, authProviders: [] });
		mount();
		expect(screen.queryByRole('button')).toBeNull();
		expect(screen.queryByTestId('account-menu')).toBeNull();
	});

	it('offers "Sign in" and opens the chooser, saying guest play keeps working', async () => {
		await load({ me: null, authProviders: [{ id: 'google', name: 'Google' }, { id: 'github', name: 'GitHub' }] });
		mount();
		await fireEvent.click(screen.getByRole('button', { name: 'Sign in' }));
		const dialog = screen.getByRole('dialog', { name: 'Sign in' });
		expect(dialog).toHaveTextContent(/keep playing as a guest/i);
		const google = within(dialog).getByRole('link', { name: 'Continue with Google' });
		expect(google).toHaveAttribute('href', '/auth/google/login?next=%2Fleaderboard%3Fmap%3Da');
		expect(google).toHaveAttribute('data-sveltekit-reload');
		expect(within(dialog).getByRole('link', { name: 'Continue with GitHub' })).toHaveAttribute('href', expect.stringContaining('/auth/github/login'));
		await fireEvent.keyDown(window, { key: 'Escape' });
		expect(screen.queryByRole('dialog')).toBeNull();
	});

	it('shows the handle with profile, runs, settings and sign out when signed in', async () => {
		await load({ me, authProviders: [{ id: 'google', name: 'Google' }] });
		mount();
		expect(screen.queryByRole('button', { name: 'Sign in' })).toBeNull();
		const button = screen.getByTestId('account-button');
		expect(button).toHaveTextContent('speedy_fox');
		expect(button).toHaveAttribute('aria-expanded', 'false');
		await fireEvent.click(button);
		expect(button).toHaveAttribute('aria-expanded', 'true');
		const menu = screen.getByRole('menu', { name: 'Account' });
		expect(within(menu).getByRole('menuitem', { name: 'My profile' })).toHaveAttribute('href', '/u/speedy_fox');
		expect(within(menu).getByRole('menuitem', { name: 'My runs' })).toHaveAttribute('href', '/motor/runs');
		expect(within(menu).getByRole('menuitem', { name: 'Settings' })).toHaveAttribute('href', '/settings');
		await fireEvent.keyDown(window, { key: 'Escape' });
		expect(screen.queryByRole('menu')).toBeNull();
	});

	it('closes on a click outside', async () => {
		await load({ me, authProviders: [] });
		mount();
		await fireEvent.click(screen.getByTestId('account-button'));
		expect(screen.getByRole('menu')).toBeInTheDocument();
		await fireEvent.click(document.body);
		expect(screen.queryByRole('menu')).toBeNull();
	});

	it('signs out through the server and falls back to "Sign in"', async () => {
		await load({ me, authProviders: [{ id: 'google', name: 'Google' }] });
		const { mutation } = mount();
		await fireEvent.click(screen.getByTestId('account-button'));
		await fireEvent.click(screen.getByRole('menuitem', { name: 'Sign out' }));
		await vi.waitFor(() => expect(screen.getByRole('button', { name: 'Sign in' })).toBeInTheDocument());
		expect(mutation).toHaveBeenCalledTimes(1);
	});
});

describe('SignInDialog', () => {
	it('lists one button per provider, says what is kept, and closes with the button', async () => {
		const onclose = vi.fn();
		render(SignInDialog, { props: { providers: [{ id: 'google', name: 'Google' }], next: '/', onclose } });
		expect(screen.getByTestId('signin-google')).toHaveAttribute('href', '/auth/google/login?next=%2F');
		expect(screen.getByRole('dialog')).toHaveTextContent(/never your email/i);
		await fireEvent.click(screen.getByRole('button', { name: 'Close' }));
		expect(onclose).toHaveBeenCalled();
	});

	it('never lets a hostile return path through', () => {
		render(SignInDialog, { props: { providers: [{ id: 'google', name: 'Google' }], next: 'https://evil.example/x', onclose: () => {} } });
		expect(screen.getByTestId('signin-google')).toHaveAttribute('href', '/auth/google/login?next=%2F');
	});
});
