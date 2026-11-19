import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
	MAX_OWN_SESSIONS,
	claimableSessions,
	forgetAllOwnerTokens,
	forgetOwnerTokens,
	ownerTokenFor,
	ownerTokens,
	rememberOwnerToken,
	rememberSession
} from './sessions';
import { isMine, playerHref } from './leaderboard';

beforeEach(() => localStorage.clear());

describe('owner tokens', () => {
	it('are remembered with the session and offered for claiming', () => {
		rememberSession('aaaaaaaaaaaaaaaa', 'BraveOtter', 'tok-a');
		rememberSession('bbbbbbbbbbbbbbbb', undefined, 'tok-b');
		rememberSession('cccccccccccccccc', undefined, null); // sessions without a token cannot be claimed
		expect(ownerTokens()).toEqual({ aaaaaaaaaaaaaaaa: 'tok-a', bbbbbbbbbbbbbbbb: 'tok-b' });
		expect(claimableSessions()).toEqual([
			{ sessionID: 'aaaaaaaaaaaaaaaa', ownerToken: 'tok-a' },
			{ sessionID: 'bbbbbbbbbbbbbbbb', ownerToken: 'tok-b' }
		]);
	});

	it('looks up the play credential for one session', () => {
		rememberOwnerToken('aaaaaaaaaaaaaaaa', 'tok-a');
		expect(ownerTokenFor('aaaaaaaaaaaaaaaa')).toBe('tok-a');
		// A session this browser did not create has no token to play with.
		expect(ownerTokenFor('bbbbbbbbbbbbbbbb')).toBeNull();
		localStorage.clear();
		expect(ownerTokenFor('aaaaaaaaaaaaaaaa')).toBeNull();
	});

	it('keeps the newest ones when there are too many', () => {
		for (let i = 0; i < MAX_OWN_SESSIONS + 5; i++) rememberOwnerToken(`id${i}`, `t${i}`);
		const ids = Object.keys(ownerTokens());
		expect(ids).toHaveLength(MAX_OWN_SESSIONS);
		expect(ids[0]).toBe('id5');
		expect(ids.at(-1)).toBe(`id${MAX_OWN_SESSIONS + 4}`);
		expect(claimableSessions(3).map((c) => c.sessionID)).toEqual([`id${MAX_OWN_SESSIONS + 2}`, `id${MAX_OWN_SESSIONS + 3}`, `id${MAX_OWN_SESSIONS + 4}`]);
	});

	it('are forgotten once handled, or all at once', () => {
		rememberOwnerToken('a', '1');
		rememberOwnerToken('b', '2');
		forgetOwnerTokens(['a']);
		expect(ownerTokens()).toEqual({ b: '2' });
		forgetAllOwnerTokens();
		expect(ownerTokens()).toEqual({});
	});

	it('survive corrupt or unavailable storage', () => {
		localStorage.setItem('drbrain-motor-programming.ownerTokens', '{not json');
		expect(ownerTokens()).toEqual({});
		localStorage.setItem('drbrain-motor-programming.ownerTokens', JSON.stringify({ ok: 'x', bad: 5, empty: '' }));
		expect(ownerTokens()).toEqual({ ok: 'x' });
		localStorage.setItem('drbrain-motor-programming.ownerTokens', '[1,2]');
		expect(ownerTokens()).toEqual({});
		vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
			throw new Error('denied');
		});
		expect(ownerTokens()).toEqual({});
		expect(claimableSessions()).toEqual([]);
		vi.restoreAllMocks();
	});
});

describe('leaderboard rows of accounts', () => {
	it('count as mine when the server says so, or when the session is this browser’s', () => {
		expect(isMine({ sessionId: 'x', mine: true }, [])).toBe(true);
		expect(isMine({ sessionId: 'x', mine: false }, ['y'])).toBe(false);
		expect(isMine({ sessionId: 'x', mine: false }, ['y', 'x'])).toBe(true);
		expect(isMine({ sessionId: 'x' }, ['x'])).toBe(true);
	});

	it('link to the profile, the runs by name, or nowhere', () => {
		expect(playerHref({ displayName: 'Ada L', handle: 'ada' })).toBe('/u/ada');
		expect(playerHref({ displayName: 'BraveOtter', handle: null })).toBe('/motor/runs?name=BraveOtter');
		expect(playerHref({ displayName: 'Private player', handle: null })).toBeNull();
		expect(playerHref({ displayName: 'Anonymous', handle: null })).toBeNull();
	});
});
