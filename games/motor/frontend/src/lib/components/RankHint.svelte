<script lang="ts">
	import { getContextClient } from '@urql/svelte';
	import { unwrap } from '$lib/api';
	import { boardHref, rankSummary } from '$lib/leaderboard';
	import { LEADERBOARD_QUERY } from '$lib/queries';
	import type { Leaderboard } from '$lib/types';

	/** Where a won session stands on its map's default (fewest slots) board. Shows nothing until known, or if the lookup fails. */
	let { mapId, sessionId }: { mapId: string; sessionId: string } = $props();

	const client = getContextClient();
	let summary = $state<string | null>(null);

	$effect(() => {
		const map = mapId;
		const session = sessionId;
		let cancelled = false;
		summary = null;
		unwrap<{ leaderboard: Leaderboard | null }>(
			client.query(LEADERBOARD_QUERY, { mapId: map, order: 'SLOTS', limit: 1, sessionId: session }, { requestPolicy: 'network-only' })
		)
			.then((d) => {
				if (!cancelled && d.leaderboard) summary = rankSummary(d.leaderboard.yours, d.leaderboard.total);
			})
			.catch(() => {
				// A missing rank is not worth an error in the middle of a win.
			});
		return () => {
			cancelled = true;
		};
	});
</script>

{#if summary}
	<p class="mt-3 text-sm text-slate-700" data-testid="rank-hint">
		{summary} ·
		<a href={boardHref(mapId)} class="font-medium text-indigo-700 underline">Leaderboard</a>
	</p>
{/if}
