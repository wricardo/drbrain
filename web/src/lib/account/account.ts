import { unwrap } from '@drbrain/ui/graphql';
import { describeError } from '@drbrain/ui/errors';
import { LOG_IN_WITH_PASSWORD_MUTATION, REGISTER_WITH_PASSWORD_MUTATION } from './queries';
import type { Client } from '@urql/svelte';
import type { Account } from './types';

/** Handles are 3-20 characters: lower-case letters, digits and underscores (the server is the authority). */
export const HANDLE_RE = /^[a-z0-9_]{3,20}$/;
export const MAX_HANDLE = 20;
export const MAX_DISPLAY_NAME = 40;

/** Lower-cases and trims a handle as typed ("@Speedy_Fox " becomes "speedy_fox"). */
export function normalizeHandle(raw: string): string {
	return raw.trim().replace(/^@/, '').toLowerCase();
}

/** A short reason why a typed handle cannot work, or null. Reserved and blocked words are only known to the server. */
export function handleProblem(raw: string): string | null {
	const h = normalizeHandle(raw);
	if (h.length < 3) return 'Use at least 3 characters.';
	if (h.length > MAX_HANDLE) return `Use at most ${MAX_HANDLE} characters.`;
	if (!HANDLE_RE.test(h)) return 'Use only lower-case letters, digits and underscores.';
	return null;
}

/** Same-site relative path for returning after sign-in; anything else becomes "/". */
export function safeNext(path: string | null | undefined): string {
	const p = path ?? '';
	if (!p.startsWith('/') || p.startsWith('//') || /[\\\u0000-\u001f\u007f]/.test(p) || p.length > 512) return '/';
	if (p === '/auth' || p.startsWith('/auth/') || p.startsWith('/auth?')) return '/';
	return p;
}

/** Where a provider's button leads. A plain navigation: the server redirects to the provider and back to `next`. */
export function signInHref(providerId: string, next: string): string {
	return `/auth/${encodeURIComponent(providerId)}/login?next=${encodeURIComponent(safeNext(next))}`;
}

/** The public profile page of a handle. */
export function profileHref(handle: string): string {
	return `/u/${encodeURIComponent(handle)}`;
}

export interface SigninNotice {
	kind: 'error' | 'info';
	message: string;
}

/** The message for the `?signin=` reason the server adds when a sign-in did not complete; null for anything else. */
export function signinNotice(value: string | null | undefined): SigninNotice | null {
	switch (value) {
		case 'cancelled':
			return { kind: 'info', message: 'Sign-in was cancelled. You can keep playing as a guest.' };
		case 'failed':
			return { kind: 'error', message: 'Sign-in did not work. Please try again; you can keep playing as a guest meanwhile.' };
		default:
			return null;
	}
}

/** The URL without the `signin` parameter (so a reload does not repeat the notice). */
export function withoutSigninParam(pathname: string, search: string): string {
	const q = new URLSearchParams(search);
	q.delete('signin');
	const rest = q.toString();
	return rest ? `${pathname}?${rest}` : pathname;
}

/** When the handle can be changed again, as a sentence; null when it can be changed now. */
export function handleChangeText(account: Pick<Account, 'handleChangeableAt'>, now: Date = new Date()): string | null {
	if (!account.handleChangeableAt) return null;
	const at = new Date(account.handleChangeableAt);
	if (Number.isNaN(at.getTime()) || at.getTime() <= now.getTime()) return null;
	const hours = Math.ceil((at.getTime() - now.getTime()) / 3_600_000);
	return hours <= 1 ? 'You can change your handle again in about an hour.' : `You can change your handle again in about ${hours} hours.`;
}

/** The provider id of username/password sign-in. It is a form, not a redirect like Google or GitHub. */
export const PASSWORD_PROVIDER = 'password';
export const MIN_PASSWORD = 8;
export const MAX_PASSWORD_BYTES = 128;

/**
 * A short reason a new password cannot work, or null. The server also rejects very common passwords
 * and is the authority; this only saves a round trip for the obvious cases.
 */
export function passwordProblem(password: string, handle = ''): string | null {
	if ([...password].length < MIN_PASSWORD) return `Use at least ${MIN_PASSWORD} characters.`;
	if (new TextEncoder().encode(password).length > MAX_PASSWORD_BYTES) return 'That password is too long.';
	if (handle && password.toLowerCase() === normalizeHandle(handle)) return 'The password cannot be the same as your username.';
	return null;
}

export type PasswordMode = 'login' | 'register';

export interface PasswordResult {
	ok: boolean;
	/** The server's reason when not ok. */
	message: string;
}

/**
 * Signs up or signs in with a username and password, through the
 * registerWithPassword / logInWithPassword mutations. The server sets the login
 * cookie on that response, exactly as the old form post did.
 *
 * The failure text comes from the GraphQL error: the server answers a wrong
 * password and an unknown username identically (UNAUTHENTICATED), and locks a
 * (address, username) pair out after repeated failures (LIMIT_REACHED) with the
 * wait spelled out in the message.
 */
export async function submitPassword(
	mode: PasswordMode,
	handle: string,
	password: string,
	client: Pick<Client, 'mutation'>
): Promise<PasswordResult> {
	try {
		await unwrap(
			client.mutation(mode === 'register' ? REGISTER_WITH_PASSWORD_MUTATION : LOG_IN_WITH_PASSWORD_MUTATION, {
				handle: normalizeHandle(handle),
				password
			})
		);
		return { ok: true, message: '' };
	} catch (err) {
		const described = describeError(err);
		const message = described.message || described.title;
		return { ok: false, message: message === 'Request failed' ? 'That did not work. Try again.' : message };
	}
}
