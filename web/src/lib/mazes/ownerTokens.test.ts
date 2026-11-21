import { beforeEach, describe, expect, it } from 'vitest';
import { MAX_OWN_SESSIONS, ownerTokenFor, ownerTokens, rememberOwnerToken } from './ownerTokens';

function clear(): void {
	localStorage.clear();
}

describe('owner tokens', () => {
	beforeEach(clear);

	it('remembers, reads and overwrites a token per session', () => {
		expect(ownerTokenFor('a')).toBeNull();
		expect(ownerTokens()).toEqual({});
		rememberOwnerToken('a', 't1');
		rememberOwnerToken('b', 't2');
		expect(ownerTokenFor('a')).toBe('t1');
		expect(ownerTokenFor('b')).toBe('t2');
		rememberOwnerToken('a', 't3');
		expect(ownerTokenFor('a')).toBe('t3');
		expect(Object.keys(ownerTokens())).toEqual(['b', 'a']);
	});

	it('keeps the newest tokens and drops the oldest past the cap', () => {
		for (let i = 0; i < MAX_OWN_SESSIONS + 5; i++) rememberOwnerToken(`s${i}`, `t${i}`);
		expect(Object.keys(ownerTokens())).toHaveLength(MAX_OWN_SESSIONS);
		expect(ownerTokenFor('s0')).toBeNull();
		expect(ownerTokenFor(`s${MAX_OWN_SESSIONS + 4}`)).toBe(`t${MAX_OWN_SESSIONS + 4}`);
	});

	it('survives corrupt or missing storage', () => {
		localStorage.setItem('drbrain-mazes.ownerTokens', '{not json');
		expect(ownerTokens()).toEqual({});
		expect(ownerTokenFor('a')).toBeNull();
		expect(() => rememberOwnerToken('a', 't')).not.toThrow();
		expect(ownerTokenFor('a')).toBe('t');
	});
});
