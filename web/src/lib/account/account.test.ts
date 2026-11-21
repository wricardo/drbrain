import { describe, expect, it, vi } from 'vitest';
import { createClient, fetchExchange } from '@urql/svelte';
import {
	handleChangeText,
	handleProblem,
	normalizeHandle,
	profileHref,
	safeNext,
	signInHref,
	signinNotice,
	submitPassword,
	withoutSigninParam
} from './account';
import { claimOffer, claimResultText, summarizeClaims, summarizeProfile, tallyText } from '$lib/motor/profile';
import type { GameMap, PlayerRun } from '$lib/motor/types';

describe('handles', () => {
	it('normalises what was typed', () => {
		expect(normalizeHandle('  @Speedy_Fox ')).toBe('speedy_fox');
		expect(normalizeHandle('')).toBe('');
	});

	it.each([
		['ab', 'at least 3'],
		['a'.repeat(21), 'at most 20'],
		['has space', 'lower-case letters'],
		['dash-ed', 'lower-case letters'],
		['émile', 'lower-case letters']
	])('explains why %j cannot work', (input, text) => {
		expect(handleProblem(input)).toContain(text);
	});

	it.each(['otter', 'Brave_Otter9', '@fox_1', 'abc', 'a'.repeat(20)])('accepts %j', (input) => {
		expect(handleProblem(input)).toBeNull();
	});
});

describe('safeNext', () => {
	it.each([
		['/play/abc', '/play/abc'],
		['/leaderboard?map=a&order=steps', '/leaderboard?map=a&order=steps'],
		['/', '/'],
		['', '/'],
		[null, '/'],
		[undefined, '/'],
		['https://evil.example', '/'],
		['//evil.example', '/'],
		['/\\evil.example', '/'],
		['evil.example', '/'],
		['/ok\nSet-Cookie: a=b', '/'],
		['/auth/google/login', '/'],
		['/authors', '/authors'],
		['/' + 'a'.repeat(600), '/']
	])('%j -> %j', (input, want) => {
		expect(safeNext(input)).toBe(want);
	});
});

describe('links', () => {
	it('builds the provider link with an encoded, validated return path', () => {
		expect(signInHref('google', '/motor/play/abc?x=1')).toBe('/auth/google/login?next=%2Fmotor%2Fplay%2Fabc%3Fx%3D1');
		expect(signInHref('github', 'https://evil.example')).toBe('/auth/github/login?next=%2F');
		expect(signInHref('we ird', '/')).toBe('/auth/we%20ird/login?next=%2F');
	});
	it('builds the profile link', () => {
		expect(profileHref('speedy_fox')).toBe('/u/speedy_fox');
	});
});

describe('sign-in notices', () => {
	it('explains a failed or cancelled sign-in and ignores anything else', () => {
		expect(signinNotice('failed')?.kind).toBe('error');
		expect(signinNotice('failed')?.message).toContain('guest');
		expect(signinNotice('cancelled')?.kind).toBe('info');
		expect(signinNotice('<script>')).toBeNull();
		expect(signinNotice(null)).toBeNull();
	});
	it('removes only the signin parameter', () => {
		expect(withoutSigninParam('/maps', '?signin=failed')).toBe('/maps');
		expect(withoutSigninParam('/leaderboard', '?map=a&signin=failed&order=steps')).toBe('/leaderboard?map=a&order=steps');
		expect(withoutSigninParam('/', '')).toBe('/');
	});
});

describe('password sessions', () => {
	it('stores the bearer token from the response', async () => {
		const fetch = vi.fn(async (_url: RequestInfo | URL, _init?: RequestInit) => new Response(
			JSON.stringify({ data: { account: { logInWithPassword: { token: 'jwt-token', expiresAt: '2026-06-02T00:00:00Z', user: { handle: 'fox' } } } } }),
			{ headers: { 'Content-Type': 'application/json' } }
		));
		const client = createClient({ url: '/graphql', exchanges: [fetchExchange], fetch });

		await expect(submitPassword('login', 'fox', 'demo password', client)).resolves.toMatchObject({ ok: true });
		expect(new Headers(fetch.mock.calls[0][1]?.headers).get('Accept')).toBe('application/json');
		expect(localStorage.getItem('drbrain.auth.token')).toBe('jwt-token');
	});
});

describe('handleChangeText', () => {
	const now = new Date('2026-06-01T12:00:00Z');
	it('is null when the handle can be changed now', () => {
		expect(handleChangeText({ handleChangeableAt: null }, now)).toBeNull();
		expect(handleChangeText({ handleChangeableAt: '2026-06-01T11:00:00Z' }, now)).toBeNull();
		expect(handleChangeText({ handleChangeableAt: 'garbage' }, now)).toBeNull();
	});
	it('says how long to wait', () => {
		expect(handleChangeText({ handleChangeableAt: '2026-06-01T12:20:00Z' }, now)).toContain('about an hour');
		expect(handleChangeText({ handleChangeableAt: '2026-06-02T10:00:00Z' }, now)).toContain('about 22 hours');
	});
});

const map = (id: string, name: string, difficulty: string, builtin: boolean) => ({ id, name, difficulty, builtin }) as GameMap;
const maps = [
	map('c1', 'Community 1', 'easy', false),
	map('e2', 'Level 2', 'easy', true),
	map('e1', 'Level 1', 'easy', true),
	map('h', 'Patterns 1', 'hard', true),
	map('c2', 'Community 2', 'hard', false)
];
const run = (mapId: string, at: string, rank = 1): PlayerRun => ({
	mapId,
	total: 4,
	entry: { rank, sessionId: 'aaaaaaaaaaaaaaaa', displayName: 'X', handle: 'x', mine: false, slots: 5, steps: 8, achievedAt: at }
});

describe('summarizeProfile', () => {
	const s = summarizeProfile(maps, [
		run('e1', '2026-01-01T00:00:00Z'),
		run('c1', '2026-01-03T00:00:00Z'),
		run('h', '2026-01-02T00:00:00Z'),
		run('gone', '2026-01-09T00:00:00Z')
	]);
	it('counts the official campaign and community maps separately', () => {
		expect(s.official).toEqual({ solved: 2, total: 3 });
		expect(s.community).toEqual({ solved: 1, total: 2 });
		expect(tallyText(s.official, 'solved')).toBe('2 of 3 solved');
	});
	it('lists solved maps in the standard order (official maps first, then community) and drops deleted maps', () => {
		expect(s.rows.map((r) => r.map.id)).toEqual(['e1', 'h', 'c1']);
	});
	it('lists the latest wins first, limited', () => {
		expect(s.recent.map((r) => r.mapId)).toEqual(['c1', 'h', 'e1']);
		expect(summarizeProfile(maps, [run('e1', '2026-01-01T00:00:00Z'), run('h', '2026-01-02T00:00:00Z')], 1).recent).toHaveLength(1);
	});
	it('handles a player with no runs', () => {
		const empty = summarizeProfile(maps, []);
		expect(empty.official).toEqual({ solved: 0, total: 3 });
		expect(empty.rows).toEqual([]);
		expect(empty.recent).toEqual([]);
	});
});

describe('claims', () => {
	it('phrases the offer', () => {
		expect(claimOffer(1)).toContain('1 guest run ');
		expect(claimOffer(3)).toContain('3 guest runs');
	});
	it('summarises the server answer', () => {
		const o = summarizeClaims([
			{ sessionID: 'a', status: 'ATTACHED' },
			{ sessionID: 'b', status: 'ALREADY_YOURS' },
			{ sessionID: 'c', status: 'REJECTED' }
		]);
		expect(o).toEqual({ attached: 1, alreadyYours: 1, rejected: 1, handled: ['a', 'b', 'c'] });
		expect(claimResultText(o)).toBe('Added 2 runs to your profile. 1 could not be added (already on another account, or not from this browser).');
		expect(claimResultText(summarizeClaims([{ sessionID: 'a', status: 'ATTACHED' }]))).toBe('Added 1 run to your profile.');
		expect(claimResultText(summarizeClaims([]))).toBe('Nothing to add.');
	});
});
