import { cleanup, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import RankHint from './RankHint.svelte';
import ResultModal from './ResultModal.svelte';
import { LEADERBOARD_QUERY } from '$lib/queries';
import type { Leaderboard, LeaderboardEntry } from '$lib/types';

// urql keeps its client in a Svelte context under this key; the test supplies a fake client there.
const CLIENT_KEY = '$$_urql';

const yours: LeaderboardEntry = {
	rank: 2,
	sessionId: 'aaaaaaaaaaaaaaaa',
	displayName: 'Ada',
	slots: 6,
	steps: 9,
	achievedAt: '2026-01-02T03:04:05Z'
};

function fakeClient(result: { data?: { leaderboard: Leaderboard | null }; error?: Error }) {
	const query = vi.fn(() => ({ toPromise: () => Promise.resolve(result) }));
	return { client: { query } as never, query };
}

function board(over: Partial<Leaderboard> = {}): Leaderboard {
	return { mapId: 'm1', order: 'SLOTS', total: 14, entries: [], yours, ...over };
}

afterEach(() => {
	cleanup();
	vi.restoreAllMocks();
});

describe('RankHint', () => {
	it('shows the rank, slots and steps with a link to the board', async () => {
		const { client, query } = fakeClient({ data: { leaderboard: board() } });
		render(RankHint, { props: { mapId: 'm1', sessionId: 'aaaaaaaaaaaaaaaa' }, context: new Map([[CLIENT_KEY, client]]) });
		expect(await screen.findByTestId('rank-hint')).toHaveTextContent('Rank 2 of 14 · 6 slots · 9 steps');
		expect(screen.getByRole('link', { name: 'Leaderboard' })).toHaveAttribute('href', '/leaderboard?map=m1');
		expect(query).toHaveBeenCalledWith(
			LEADERBOARD_QUERY,
			{ mapId: 'm1', order: 'SLOTS', limit: 1, sessionId: 'aaaaaaaaaaaaaaaa' },
			{ requestPolicy: 'network-only' }
		);
	});

	it.each([
		['the session has no entry', { data: { leaderboard: board({ yours: null }) } }],
		['the map is gone', { data: { leaderboard: null } }],
		['the request fails', { error: new Error('offline') }]
	])('shows nothing when %s', async (_name, result) => {
		const { client, query } = fakeClient(result);
		render(RankHint, { props: { mapId: 'm1', sessionId: 'aaaaaaaaaaaaaaaa' }, context: new Map([[CLIENT_KEY, client]]) });
		await waitFor(() => expect(query).toHaveBeenCalled());
		await Promise.resolve();
		expect(screen.queryByTestId('rank-hint')).toBeNull();
	});
});

describe('ResultModal rank hint', () => {
	const props = { reason: null, steps: 9, maxSteps: 40, onreset: () => {}, onclose: () => {} };

	it('asks for the rank only after a win and only with a map and session', async () => {
		const { client, query } = fakeClient({ data: { leaderboard: board() } });
		const context = new Map([[CLIENT_KEY, client]]);

		render(ResultModal, { props: { ...props, status: 'LOST', mapId: 'm1', sessionId: 's' }, context });
		render(ResultModal, { props: { ...props, status: 'WON' }, context });
		expect(query).not.toHaveBeenCalled();

		cleanup();
		render(ResultModal, { props: { ...props, status: 'WON', mapId: 'm1', sessionId: 'aaaaaaaaaaaaaaaa' }, context });
		expect(await screen.findByTestId('rank-hint')).toHaveTextContent('Rank 2 of 14');
	});
});
