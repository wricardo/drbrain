<script lang="ts">
	import { pageTitle } from '$lib/mazes/brand';
	import { onDestroy, onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { getContextClient } from '@urql/svelte';
	import { unwrap } from '@drbrain/ui/graphql';
	import { describeError, type DescribedError } from '$lib/mazes/errors';
	import { sortMapsByDifficulty } from '$lib/mazes/maps';
	import { MAPS_QUERY, SESSIONS_QUERY } from '$lib/mazes/queries';
	import {
		STATUS_FILTERS,
		STATUS_FILTER_LABEL,
		countByStatus,
		filterSessions,
		isFinished,
		sortSessions,
		statusText,
		type SessionSort,
		type StatusFilter
	} from '$lib/mazes/sessions';
	import { absoluteTime, relativeTime } from '$lib/mazes/time';
	import type { GameMap, SessionSummary } from '$lib/mazes/types';

	/** Server-side maximum for the sessions query. */
	const LIMIT = 500;
	/** Rows added by each "Load more". */
	const PAGE = 25;
	const REFRESH_MS = 10_000;

	const client = getContextClient();

	let sessions = $state<SessionSummary[]>([]);
	let maps = $state<Pick<GameMap, 'id' | 'name' | 'difficulty'>[]>([]);
	let loading = $state(true);
	let refreshing = $state(false);
	let error = $state<DescribedError | null>(null);
	let now = $state(new Date());

	let sort = $state<SessionSort>('RECENT');
	let mapId = $state('');
	let status = $state<StatusFilter>('all');
	let text = $state('');
	let visible = $state(PAGE);
	let selected = $state<string[]>([]);

	const counts = $derived(countByStatus(sessions));
	const shown = $derived(sortSessions(filterSessions(sessions, { status, text }), sort));
	const page = $derived(shown.slice(0, visible));

	/** Loads the list; `quiet` (auto-refresh) keeps the table on screen instead of a loading line. */
	async function load(quiet = false) {
		if (quiet) refreshing = true;
		else loading = true;
		error = null;
		try {
			const data = await unwrap<{ sessions: SessionSummary[] }>(
				client.query(
					SESSIONS_QUERY,
					{ limit: LIMIT, sort: sort === 'CREATED' ? 'CREATED' : 'RECENT', mapId: mapId || null },
					{ requestPolicy: 'network-only' }
				)
			);
			sessions = data.sessions;
			now = new Date();
		} catch (e) {
			error = describeError(e);
		} finally {
			loading = false;
			refreshing = false;
		}
	}

	let timer: ReturnType<typeof setInterval> | undefined;
	onMount(async () => {
		try {
			const m = await unwrap<{ maps: GameMap[] }>(client.query(MAPS_QUERY, {}));
			maps = sortMapsByDifficulty(m.maps);
		} catch (e) {
			error = describeError(e);
		}
		await load();
		timer = setInterval(() => {
			if (!document.hidden) void load(true);
		}, REFRESH_MS);
	});
	onDestroy(() => clearInterval(timer));

	function toggle(id: string, on: boolean) {
		selected = on ? [...selected, id] : selected.filter((x) => x !== id);
	}

	function watchSelected() {
		void goto(`/mazes/multi?ids=${selected.map(encodeURIComponent).join(',')}`);
	}

	const statusClass: Record<string, string> = {
		won: 'bg-emerald-100 text-emerald-900',
		lost: 'bg-rose-100 text-rose-900',
		playing: 'bg-indigo-600 text-white',
		ready: 'bg-indigo-50 text-indigo-800'
	};
</script>

<svelte:head><title>{pageTitle('Sessions')}</title></svelte:head>

<div class="mx-auto max-w-6xl px-4 sm:px-6 py-8 space-y-5">
	<header class="flex flex-wrap items-start justify-between gap-3">
		<div>
			<h1 class="text-2xl font-semibold text-indigo-950">Sessions</h1>
			<p class="mt-1 text-sm text-slate-700">Every run on this server. Anyone can watch any session; only its creator can move the miner.</p>
		</div>
		<div class="flex flex-wrap items-center gap-2">
			<span class="basis-full text-xs text-slate-600 sm:basis-auto" data-testid="auto-refresh">{refreshing ? 'Refreshing…' : 'Auto-refreshes every 10 s'}</span>
			<button
				type="button"
				onclick={() => load()}
				disabled={loading}
				class="db-button" data-variant="secondary"
				>Refresh</button
			>
			<button
				type="button"
				onclick={watchSelected}
				disabled={selected.length === 0}
				data-testid="watch-selected"
				class="db-button"
				>Watch selected ({selected.length})</button
			>
		</div>
	</header>

	<div class="flex flex-wrap gap-1.5" role="group" aria-label="Status">
		{#each STATUS_FILTERS as s (s)}
			<button
				type="button"
				aria-pressed={status === s}
				onclick={() => {
					status = s;
					visible = PAGE;
				}}
				data-testid="status-{s}"
				class="rounded-full border px-3 py-1.5 text-sm transition-colors {status === s
					? 'border-indigo-600 bg-indigo-600 text-white'
					: 'border-indigo-200 bg-white text-indigo-900 hover:bg-indigo-50'}">{STATUS_FILTER_LABEL[s]} <span class="opacity-75">({counts[s]})</span></button
			>
		{/each}
	</div>

	<form class="grid gap-3 sm:grid-cols-3" onsubmit={(e) => e.preventDefault()} aria-label="Filter sessions">
		<div>
			<label for="f-text" class="block text-sm font-medium text-indigo-900">Search</label>
			<input
				id="f-text"
				type="search"
				bind:value={text}
				oninput={() => (visible = PAGE)}
				placeholder="Search name, id or map"
				class="db-input mt-1"
			/>
		</div>
		<div>
			<label for="f-map" class="block text-sm font-medium text-indigo-900">Map</label>
			<select
				id="f-map"
				bind:value={mapId}
				onchange={() => {
					visible = PAGE;
					load();
				}}
				class="db-input mt-1"
			>
				<option value="">All maps</option>
				{#each maps as m (m.id)}
					<option value={m.id}>{m.name}</option>
				{/each}
			</select>
		</div>
		<div>
			<label for="f-sort" class="block text-sm font-medium text-indigo-900">Sort</label>
			<select
				id="f-sort"
				bind:value={sort}
				onchange={() => {
					visible = PAGE;
					load();
				}}
				class="db-input mt-1"
			>
				<option value="RECENT">Last activity</option>
				<option value="MOVES">Most moves</option>
				<option value="CREATED">Newest created</option>
			</select>
		</div>
	</form>

	{#if error}
		<p role="alert" class="rounded-lg border border-rose-300 bg-rose-50 px-3 py-2 text-sm text-rose-900">
			<strong>{error.title}.</strong> {error.message}
		</p>
	{/if}

	<p class="text-sm text-slate-600" role="status" aria-live="polite">
		{#if loading}
			Loading sessions…
		{:else}
			Showing {page.length} of {shown.length}{shown.length !== sessions.length ? ` matching (${sessions.length} loaded)` : ''}{sessions.length >= LIMIT
				? ` — the server returns at most ${LIMIT}; narrow by map to see others`
				: ''}.
		{/if}
	</p>

	{#if !loading && shown.length === 0}
		<p class="rounded-xl border border-indigo-200 bg-white px-4 py-6 text-sm text-slate-600">
			{#if sessions.length === 0}
				No sessions yet.
				<a href="/mazes" class="ml-2 inline-block rounded-lg bg-indigo-600 px-3 py-1.5 font-semibold text-white hover:bg-indigo-700">Start a map</a>
			{:else}
				No sessions match these filters.
				<button
					type="button"
					class="ml-2 rounded-lg bg-indigo-600 px-3 py-1.5 font-semibold text-white hover:bg-indigo-700"
					onclick={() => {
						status = 'all';
						text = '';
					}}>Clear filters</button
				>
			{/if}
		</p>
	{:else if shown.length > 0}
		<div class="db-panel min-w-0 max-w-full overflow-x-auto">
			<table class="w-full text-left text-sm">
				<caption class="sr-only">Sessions</caption>
				<thead class="bg-indigo-50 text-xs uppercase tracking-wide text-indigo-900">
					<tr>
						<th scope="col" class="w-8 px-2 py-2 sm:px-4"><span class="sr-only">Select</span></th>
						<th scope="col" class="px-2 py-2 sm:px-4">Player</th>
						<th scope="col" class="hidden px-4 py-2 sm:table-cell">Map</th>
						<th scope="col" class="px-2 py-2 sm:px-4">Status</th>
						<th scope="col" class="hidden px-4 py-2 sm:table-cell">Progress</th>
						<th scope="col" class="hidden px-4 py-2 sm:table-cell">{sort === 'CREATED' ? 'Created' : 'Last activity'}</th>
						<th scope="col" class="px-2 py-2 sm:px-4"><span class="sr-only">Links</span></th>
					</tr>
				</thead>
				<tbody class="divide-y divide-indigo-100">
					{#each page as s (s.id)}
						{@const st = statusText(s)}
						{@const at = sort === 'CREATED' ? s.createdAt : s.lastActionAt}
						<tr class={selected.includes(s.id) ? 'bg-indigo-50/60' : ''}>
							<td class="px-2 py-2 sm:px-4">
								<input
									type="checkbox"
									class="h-4 w-4 accent-indigo-600"
									checked={selected.includes(s.id)}
									onchange={(e) => toggle(s.id, e.currentTarget.checked)}
									aria-label="Select {s.displayName || 'Anonymous'} on {s.map.name}"
									data-testid="select-{s.id}"
								/>
							</td>
							<td class="break-words px-2 py-2 font-medium sm:px-4">
								{s.displayName || 'Anonymous'}
								<span class="mt-0.5 block text-xs text-slate-600 sm:hidden">{s.map.name} · {s.state.moves} / {s.map.maxMoves} moves</span>
							</td>
							<td class="hidden px-4 py-2 sm:table-cell">{s.map.name}</td>
							<td class="px-2 py-2 sm:px-4">
								<span class="rounded-full px-2 py-0.5 text-xs {statusClass[st]}">{st}</span>
							</td>
							<td class="hidden px-4 py-2 tabular-nums text-slate-700 sm:table-cell">{s.state.moves} / {s.map.maxMoves} moves{s.map.parMoves !== null ? ` · par ${s.map.parMoves}` : ''}</td>
							<td class="hidden px-4 py-2 text-slate-700 sm:table-cell"><span title={absoluteTime(at)}>{relativeTime(at, now)}</span></td>
							<td class="px-2 py-2 text-right sm:whitespace-nowrap sm:px-4">
								<a class="font-semibold text-indigo-700 underline" href="/mazes/play/{s.id}"
									>{isFinished(s) ? 'View' : 'Open'}</a
								>
							</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
		{#if page.length < shown.length}
			<p class="text-center">
				<button
					type="button"
					onclick={() => (visible += PAGE)}
					data-testid="load-more"
					class="rounded-full border border-indigo-300 bg-white px-5 py-2 text-sm font-semibold text-indigo-900 hover:bg-indigo-50"
					>Load more ({shown.length - page.length} left)</button
				>
			</p>
		{/if}
	{/if}
</div>
