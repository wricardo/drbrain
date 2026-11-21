import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
	MAX_OWN_SESSIONS,
	claimableSessions,
	forgetAllOwnerTokens,
	forgetOwnerTokens,
	forgetSession,
	lastSessionId,
	ownSessionIds,
	ownerTokenFor,
	ownerTokens,
	rememberOwnerToken,
	rememberSession
} from './ownerTokens';

beforeEach(() => localStorage.clear());
afterEach(() => vi.restoreAllMocks());

describe('last session', () => {
	it('is remembered and forgotten', () => {
		expect(lastSessionId()).toBeNull();
		rememberSession('s1');
		rememberSession('s2');
		expect(lastSessionId()).toBe('s2');
		forgetSession();
		expect(lastSessionId()).toBeNull();
	});

	it('uses Delivery-namespaced keys', () => {
		rememberSession('s1', 'tok');
		expect(localStorage.getItem('drbrain-delivery.lastSession')).toBe('s1');
		expect(localStorage.getItem('drbrain-delivery.ownSessions')).toBe('["s1"]');
		expect(localStorage.getItem('drbrain-delivery.ownerTokens')).toBe('{"s1":"tok"}');
	});
});

describe('owner tokens', () => {
	it('are stored and retrieved; other sessions have none', () => {
		rememberOwnerToken('a', 'tok-a');
		expect(ownerTokenFor('a')).toBe('tok-a');
		expect(ownerTokenFor('b')).toBeNull();
		expect(ownerTokens()).toEqual({ a: 'tok-a' });
	});

	it('are remembered with the session, unless absent', () => {
		rememberSession('a', 'tok-a');
		rememberSession('b', null);
		rememberSession('c');
		expect(ownerTokens()).toEqual({ a: 'tok-a' });
	});

	it('keep the newest MAX_OWN_SESSIONS', () => {
		for (let i = 0; i < MAX_OWN_SESSIONS + 5; i++) rememberOwnerToken(`id${i}`, `t${i}`);
		const ids = Object.keys(ownerTokens());
		expect(MAX_OWN_SESSIONS).toBe(200);
		expect(ids).toHaveLength(200);
		expect(ids[0]).toBe('id5');
		expect(ids.at(-1)).toBe('id204');
	});

	it('re-remembering moves a session to newest without duplicating', () => {
		rememberOwnerToken('a', '1');
		rememberOwnerToken('b', '2');
		rememberOwnerToken('a', '3');
		expect(Object.entries(ownerTokens())).toEqual([
			['b', '2'],
			['a', '3']
		]);
	});

	it('map to claimable sessions, newest max', () => {
		rememberOwnerToken('a', '1');
		rememberOwnerToken('b', '2');
		rememberOwnerToken('c', '3');
		expect(claimableSessions()).toEqual([
			{ sessionID: 'a', ownerToken: '1' },
			{ sessionID: 'b', ownerToken: '2' },
			{ sessionID: 'c', ownerToken: '3' }
		]);
		expect(claimableSessions(2)).toEqual([
			{ sessionID: 'b', ownerToken: '2' },
			{ sessionID: 'c', ownerToken: '3' }
		]);
	});

	it('forgetOwnerTokens removes only the given ids; forgetAll removes everything', () => {
		rememberOwnerToken('a', '1');
		rememberOwnerToken('b', '2');
		rememberOwnerToken('c', '3');
		forgetOwnerTokens(['a', 'c']);
		expect(ownerTokens()).toEqual({ b: '2' });
		forgetAllOwnerTokens();
		expect(ownerTokens()).toEqual({});
	});

	it('survive corrupt JSON in every key', () => {
		localStorage.setItem('drbrain-delivery.ownerTokens', '{not json');
		localStorage.setItem('drbrain-delivery.ownSessions', '{not json');
		expect(ownerTokens()).toEqual({});
		expect(ownSessionIds()).toEqual([]);
		expect(claimableSessions()).toEqual([]);
		expect(ownerTokenFor('a')).toBeNull();
		localStorage.setItem('drbrain-delivery.ownerTokens', '[1,2]');
		expect(ownerTokens()).toEqual({});
		localStorage.setItem('drbrain-delivery.ownerTokens', JSON.stringify({ ok: 'x', bad: 5, empty: '' }));
		expect(ownerTokens()).toEqual({ ok: 'x' });
		localStorage.setItem('drbrain-delivery.lastSession', '');
		expect(lastSessionId()).toBe('');
		// writes recover from corrupt state
		expect(() => rememberSession('n', 't')).not.toThrow();
		expect(ownSessionIds()).toEqual(['n']);
	});

	it('never throw when storage is unavailable', () => {
		const deny = () => {
			throw new Error('denied');
		};
		vi.spyOn(Storage.prototype, 'getItem').mockImplementation(deny);
		vi.spyOn(Storage.prototype, 'setItem').mockImplementation(deny);
		vi.spyOn(Storage.prototype, 'removeItem').mockImplementation(deny);
		expect(() => {
			rememberSession('a', 't');
			rememberOwnerToken('a', 't');
			forgetOwnerTokens(['a']);
			forgetAllOwnerTokens();
			forgetSession();
		}).not.toThrow();
		expect(ownerTokens()).toEqual({});
		expect(ownSessionIds()).toEqual([]);
		expect(lastSessionId()).toBeNull();
	});
});

describe('own session ids', () => {
	it('are oldest first and de-duplicated', () => {
		rememberSession('a');
		rememberSession('b');
		rememberSession('a');
		expect(ownSessionIds()).toEqual(['b', 'a']);
	});

	it('keep the newest MAX_OWN_SESSIONS', () => {
		for (let i = 0; i < MAX_OWN_SESSIONS + 3; i++) rememberSession(`id${i}`);
		const ids = ownSessionIds();
		expect(ids).toHaveLength(MAX_OWN_SESSIONS);
		expect(ids[0]).toBe('id3');
		expect(ids.at(-1)).toBe('id202');
	});

	it('ignore non-string entries', () => {
		localStorage.setItem('drbrain-delivery.ownSessions', JSON.stringify(['a', 5, null, { x: 1 }, 'b']));
		expect(ownSessionIds()).toEqual(['a', 'b']);
	});
});
