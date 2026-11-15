<script lang="ts">
	import { pageTitle } from '$lib/brand';
	import { onDestroy, onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/stores';
	import { getContextClient } from '@urql/svelte';
	import { unwrap } from '$lib/api';
	import { describeError, type DescribedError } from '$lib/errors';
	import {
		ORDER_OPTIONS,
		PAGE_LIMIT,
		boardHref,
		isMine,
		orderOption,
		playerHref,
		parseOrder,
		pickMap,
		rankBadgeClass,
		rankLabel,
		slotsText,
		stepsText
	} from '$lib/leaderboard';
	import { sortMaps } from '$lib/maps';
	import { CREATE_SESSION_MUTATION, LEADERBOARD_QUERY, MAPS_QUERY } from '$lib/queries';
	import { ownSessionIds, rememberSession, savedDisplayName } from '$lib/sessions';
	import { absoluteTime, relativeTime } from '$lib/time';
	import type { GameMap, Leaderboard, LeaderboardOrder, Session } from '$lib/types';

	const REFRESH_MS = 15_000;

	const client = getContextClient();
	let maps = $state<GameMap[]>([]);
	let mapId = $state('');
	let order = $state<LeaderboardOrder>(parseOrder($page.url.searchParams.get('order')));
	let board = $state<Leaderboard | null>(null);
	let loading = $state(true);
	let busy = $state(false);
	let error = $state<DescribedError | null>(null);
	let now = $state(new Date());
	let own = $state<string[]>([]);

	const map = $derived(maps.find((m) => m.id === mapId) ?? null);
	const option = $derived(orderOption(order));
	const totalSlots = $derived(map ? map.mainTapeLength + map.subTapeLengths.reduce((a, b) => a + b, 0) : 0);

	// Only the latest request may update the page (the map or board can change mid-flight).
	let requestId = 0;
	async function load(quiet = false) {
		if (!mapId) return;
		const mine = ++requestId;
		if (!quiet) loading = true;
		try {
			const data = await unwrap<{ leaderboard: Leaderboard | null }>(
				client.query(LEADERBOARD_QUERY, { mapId, order, limit: PAGE_LIMIT }, { requestPolicy: 'network-only' })
			);
			if (mine !== requestId) return;
			board = data.leaderboard;
			error = null;
			now = new Date();
		} catch (e) {
			if (mine === requestId) error = describeError(e);
		} finally {
			if (mine === requestId) loading = false;
		}
	}

	function show(nextMap: string, nextOrder: LeaderboardOrder) {
		mapId = nextMap;
		order = nextOrder;
		board = null;
		void goto(boardHref(nextMap, nextOrder), { replaceState: true, noScroll: true, keepFocus: true });
		void load();
	}

	let timer: ReturnType<typeof setInterval> | undefined;
	onMount(async () => {
		own = ownSessionIds();
		try {
			maps = sortMaps((await unwrap<{ maps: GameMap[] }>(client.query(MAPS_QUERY, {}))).maps);
			const first = pickMap(maps, $page.url.searchParams.get('map'));
			if (first) show(first.id, order);
			else loading = false;
		} catch (e) {
			error = describeError(e);
			loading = false;
		}
		timer = setInterval(() => void load(true), REFRESH_MS);
	});
	onDestroy(() => clearInterval(timer));

	async function play() {
		if (!map) return;
		busy = true;
		error = null;
		try {
			const data = await unwrap<{ createSession: Session }>(
				client.mutation(CREATE_SESSION_MUTATION, { mapID: map.id, displayName: savedDisplayName().trim() || null })
			);
			rememberSession(data.createSession.id, undefined, data.createSession.ownerToken);
			await goto(`/play/${data.createSession.id}`);
		} catch (e) {
			error = describeError(e);
		} finally {
			busy = false;
		}
	}
</script>

<svelte:head><title>{pageTitle('Leaderboard')}</title></svelte:head>

<div class="mx-auto max-w-6xl px-4 sm:px-6 py-8 space-y-5">
	<header>
		<h1 class="text-2xl font-semibold text-indigo-950">Leaderboard</h1>
		<p class="mt-1 text-sm text-slate-700">
			The tapes are tiny, so the best programs are the smallest. Each map ranks its winners by the slots their program uses and the steps its
			run took. The server records both when a run is won, so they cannot be edited.
		</p>
	</header>

	<div class="flex flex-wrap items-center gap-3">
		<label class="flex items-center gap-2 text-sm text-indigo-900" for="lb-map">
			Map
			<select
				id="lb-map"
				value={mapId}
				onchange={(e) => show(e.currentTarget.value, order)}
				class="rounded-lg border border-indigo-300 bg-white px-3 py-1.5 text-sm"
				data-testid="lb-map"
			>
				{#each maps as m (m.id)}
					<option value={m.id}>{m.name}</option>
				{/each}
			</select>
		</label>
		<div class="flex flex-wrap gap-1.5" role="group" aria-label="Ranking">
			{#each ORDER_OPTIONS as o (o.order)}
				<button
					type="button"
					aria-pressed={order === o.order}
					class="rounded-full border px-3 py-1 text-sm {order === o.order
						? 'border-indigo-600 bg-indigo-600 text-white'
						: 'border-indigo-200 bg-white text-indigo-900 hover:bg-indigo-50'}"
					onclick={() => show(mapId, o.order)}>{o.label}</button
				>
			{/each}
		</div>
	</div>

	{#if map}
		<p class="text-xs text-slate-600">
			{option.rule}
			{#if totalSlots}
				This map offers {slotsText(totalSlots)} in all and allows {stepsText(map.maxSteps)}.
			{/if}
		</p>
	{/if}

	{#if error}
		<p role="alert" class="rounded-lg border border-rose-300 bg-rose-50 px-3 py-2 text-sm text-rose-900">
			<strong>{error.title}.</strong> {error.message}
		</p>
	{/if}

	<p class="text-sm text-slate-600" role="status" aria-live="polite">
		{#if loading}
			Loading the leaderboard…
		{:else if board}
			{board.total === 0 ? 'No wins yet.' : `${board.total} ${board.total === 1 ? 'player has' : 'players have'} won this map${board.total > board.entries.length ? ` (showing the top ${board.entries.length})` : ''}.`}
		{/if}
	</p>

	{#if !loading && board && board.entries.length === 0}
		<div class="rounded-xl border border-indigo-200 bg-white px-4 py-6 text-sm text-slate-600" data-testid="lb-empty">
			Nobody has won {map?.name ?? 'this map'} yet. Be the first!
			<button
				type="button"
				class="ml-2 rounded-full bg-indigo-600 px-4 py-1.5 font-semibold text-white hover:bg-indigo-700 disabled:opacity-60"
				disabled={busy}
				onclick={play}>Play this map</button
			>
		</div>
	{:else if board && board.entries.length > 0}
		<div class="relative overflow-x-auto rounded-xl border border-indigo-200 bg-white">
			<table class="w-full text-left text-sm" data-testid="lb-table">
				<caption class="sr-only">{map?.name ?? 'Map'} leaderboard, {option.label.toLowerCase()} first</caption>
				<thead class="bg-indigo-50 text-xs uppercase tracking-wide text-indigo-900">
					<tr>
						<th scope="col" class="px-4 py-2">Rank</th>
						<th scope="col" class="px-4 py-2">Player</th>
						<th scope="col" class="px-4 py-2 text-right {order === 'SLOTS' ? 'text-indigo-950' : ''}">Slots</th>
						<th scope="col" class="px-4 py-2 text-right {order === 'STEPS' ? 'text-indigo-950' : ''}">Steps</th>
						<th scope="col" class="px-4 py-2">When</th>
						<th scope="col" class="px-4 py-2"><span class="sr-only">Actions</span></th>
					</tr>
				</thead>
				<tbody class="divide-y divide-indigo-100">
					{#each board.entries as e (e.sessionId)}
						{@const mine = isMine(e, own)}
						<tr class={mine ? 'bg-indigo-50/70' : ''} data-mine={mine ? 'true' : undefined}>
							<td class="px-4 py-2">
								<span
									class="inline-flex h-7 min-w-7 items-center justify-center rounded-full border px-2 text-xs font-semibold tabular-nums {rankBadgeClass(e.rank)}"
									aria-label={rankLabel(e.rank)}>{e.rank}</span
								>
							</td>
							<td class="px-4 py-2 font-medium">
								{#if playerHref(e)}
									<a href={playerHref(e)} class="hover:underline" title={e.handle ? `${e.displayName}'s profile` : `See ${e.displayName}'s runs`}>{e.displayName}</a>
								{:else}
									{e.displayName}
								{/if}
								{#if mine}
									<span class="ml-1 rounded-full bg-indigo-600 px-2 py-0.5 text-xs font-medium text-white">You</span>
								{/if}
							</td>
							<td class="px-4 py-2 text-right tabular-nums {order === 'SLOTS' ? 'font-semibold' : 'text-slate-700'}">{e.slots}</td>
							<td class="px-4 py-2 text-right tabular-nums {order === 'STEPS' ? 'font-semibold' : 'text-slate-700'}">{e.steps}</td>
							<td class="px-4 py-2 text-slate-700" title={absoluteTime(e.achievedAt)}>{relativeTime(e.achievedAt, now)}</td>
							<td class="px-4 py-2 whitespace-nowrap text-right">
								<a class="text-indigo-700 underline" href="/watch/{e.sessionId}" aria-label="Watch {e.displayName}'s run">Watch</a>
							</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
		<p class="text-xs text-slate-600">
			Players who tie on both numbers share a rank. Each guest session appears once, and a signed-in player appears once, with their best result:
			win again with a smaller program to move up.
			Agents can read this board with the <code class="rounded bg-indigo-50 px-1">leaderboard</code> query (see
			<a href="/llms.txt" class="text-indigo-700 underline">/llms.txt</a>).
		</p>
	{/if}
</div>
