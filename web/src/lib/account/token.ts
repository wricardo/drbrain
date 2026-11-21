// Access-token storage. The server is stateless (no cookies, no refresh
// tokens), so the token is the whole session: it is kept in localStorage so a
// reload stays signed in, and it is discarded on sign-out or when the server
// answers UNAUTHENTICATED.

const TOKEN_KEY = 'drbrain.auth.token';

/** Reads a value from localStorage without throwing in private mode / SSR. */
function read(key: string): string | null {
	try {
		return globalThis.localStorage?.getItem(key) ?? null;
	} catch {
		return null;
	}
}

function write(key: string, value: string | null): void {
	try {
		if (value === null) globalThis.localStorage?.removeItem(key);
		else globalThis.localStorage?.setItem(key, value);
	} catch {
		// storage unavailable: the session lasts for this page load
	}
}

/** The current bearer token, or null when signed out. */
export function authToken(): string | null {
	return read(TOKEN_KEY);
}

/** Remembers a freshly issued token. */
export function saveAuthToken(token: string): void {
	write(TOKEN_KEY, token);
}

/** Forgets the token (sign-out, or a token the server rejected). */
export function clearAuthToken(): void {
	write(TOKEN_KEY, null);
}

/** Authorization header for GraphQL requests; empty when signed out. */
export function authHeaders(): Record<string, string> {
	const token = authToken();
	return token ? { Authorization: `Bearer ${token}` } : {};
}

/** connection_init payload for graphql-ws; empty when signed out. */
export function wsConnectionParams(): Record<string, string> {
	return authHeaders();
}

/**
 * Exchanges the one-time `signin_code` a completed OAuth callback put on the
 * URL for an access token, and stores it. Returns true when a code was present
 * and redeemed; a bad or expired code gives false (the user stays signed out).
 */
export async function redeemSigninCode(code: string): Promise<boolean> {
	try {
		const res = await fetch('/auth/token', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
			body: JSON.stringify({ code })
		});
		if (!res.ok) return false;
		const body = (await res.json()) as { token?: string };
		if (!body.token) return false;
		saveAuthToken(body.token);
		return true;
	} catch {
		return false;
	}
}
