<script lang="ts">
	import { pageTitle } from '$lib/motor/brand';
	import { goto } from '$app/navigation';
	import { page } from '$app/stores';
	import { getContextClient } from '@urql/svelte';
	import { unwrap } from '$lib/motor/api';
	import BoardPanel from '$lib/motor/components/BoardPanel.svelte';
	import Stats from '$lib/motor/components/Stats.svelte';
	import TileLegend from '$lib/motor/components/TileLegend.svelte';
	import { describeError, type DescribedError } from '$lib/motor/errors';
	import { recursionChip } from '$lib/motor/recursion';
	import { CREATE_SESSION_MUTATION, MAP_QUERY } from '$lib/motor/queries';
	import { newSessionName, rememberSession, statusLine } from '$lib/motor/sessions';
	import { account } from '$lib/motor/stores/account';
	import type { GameMap, Session } from '$lib/motor/types';

	const client = getContextClient();
	const id = $derived($page.params.id ?? '');

	let map = $state<GameMap | null>(null);
	let loading = $state(true);
	let busy = $state(false);
	let error = $state<DescribedError | null>(null);

	// The same columns as /play and /watch, filled with a map nobody has played yet: the
	// counters read zero and the status is "Ready", so pressing Play only starts the run.
	const preview = $derived(map ? { map, vm: { steps: 0, treatsRemaining: map.treats }, bestSteps: null, attempts: 0 } : null);
	const readyLine = $derived(statusLine({ playing: false, vm: { status: 'READY', steps: 0, lossReason: null } }));
	const recursion = $derived(map ? recursionChip(map) : null);
	const totalSlots = $derived(map ? map.mainTapeLength + map.subTapeLengths.reduce((a, b) => a + b, 0) : 0);

	// Only the latest request may update the page (the route id can change mid-flight).
	let requestId = 0;
	async function load(mapId: string) {
		const mine = ++requestId;
		loading = true;
		error = null;
		map = null;
		try {
			const data = await unwrap<{ map: GameMap | null }>(
				client.query(MAP_QUERY, { id: mapId }, { requestPolicy: 'network-only' })
			);
			if (mine === requestId) map = data.map;
		} catch (e) {
			if (mine === requestId) error = describeError(e);
		} finally {
			if (mine === requestId) loading = false;
		}
	}

	$effect(() => {
		const mapId = id;
		if (mapId) void load(mapId);
	});

	async function play(m: GameMap) {
		busy = true;
		error = null;
		try {
			const data = await unwrap<{ createSession: Session }>(
				client.mutation(CREATE_SESSION_MUTATION, { mapID: m.id, displayName: newSessionName(!!$account.me) })
			);
			rememberSession(data.createSession.id, data.createSession.displayName, data.createSession.ownerToken);
			await goto(`/play/${data.createSession.id}`);
		} catch (e) {
			error = describeError(e);
		} finally {
			busy = false;
		}
	}
</script>

<svelte:head><title>{pageTitle(map ? map.name : 'Map')}</title></svelte:head>

<div class="mx-auto max-w-6xl px-4 sm:px-6 py-6">
	{#if loading}
		<p role="status" class="text-sm text-slate-600">Loading map…</p>
	{:else if !map}
		<div role="alert" class="rounded-lg border border-rose-300 bg-rose-50 p-4 text-sm text-rose-900">
			<strong>{error?.title ?? 'Map not found'}.</strong>
			{error?.message ?? 'No map with this id.'}
			<a href="/motor/maps" class="ml-2 underline">Back to maps</a>
		</div>
	{:else}
		<div class="flex flex-wrap items-center justify-between gap-2">
			<div>
				<h1 class="text-2xl font-semibold text-indigo-950">{map.name}</h1>
				<p class="text-sm text-slate-600">
					{map.difficulty} · {map.builtin ? 'official map' : 'player-made map'} · {map.width}×{map.height} ·
					<a class="underline" href="/motor/maps">all maps</a>
				</p>
			</div>
		</div>

		{#if error}
			<p role="alert" class="mt-3 rounded-lg border border-rose-300 bg-rose-50 px-3 py-2 text-sm text-rose-900" data-testid="action-error">
				<strong>{error.title}</strong>{#if error.code}<span class="ml-1 font-mono text-xs">[{error.code}]</span>{/if}
				{#if error.message}— {error.message}{/if}
			</p>
		{/if}

		<div class="mt-4 grid gap-6 lg:grid-cols-[minmax(0,1fr)_minmax(0,28rem)]">
			<BoardPanel {map} pinned />

			<section aria-label="Start" class="min-w-0 space-y-3 sm:space-y-4">
				<div class="flex flex-wrap items-center gap-x-3 gap-y-1 sm:block sm:space-y-4">
					<p class="rounded-lg bg-indigo-50 px-3 py-2 text-sm font-semibold text-indigo-900 max-sm:rounded-md max-sm:px-2 max-sm:py-1 max-sm:text-xs" data-testid="status-line">
						{readyLine}
					</p>
					{#if preview}
						<Stats session={preview} />
					{/if}
				</div>

				<div class="rounded-xl border border-indigo-200 bg-white p-3 space-y-3" data-testid="start-controls">
					<button
						type="button"
						class="w-full rounded-lg bg-indigo-600 px-4 py-2 text-sm font-semibold text-white hover:bg-indigo-700 disabled:opacity-50"
						disabled={busy}
						onclick={() => map && play(map)}
						data-testid="play-map">{busy ? 'Creating…' : 'Play'}</button
					>
					<div class="flex gap-2">
						<a
							href="/motor/leaderboard?map={encodeURIComponent(map.id)}"
							class="flex-1 rounded-lg border border-indigo-300 px-3 py-1.5 text-center text-sm text-indigo-800 hover:bg-indigo-50">Leaderboard</a
						>
						<a
							href="/motor/editor?map={encodeURIComponent(map.id)}"
							class="flex-1 rounded-lg border border-indigo-300 px-3 py-1.5 text-center text-sm text-indigo-800 hover:bg-indigo-50">Edit</a
						>
						<a
							href="/motor/editor?map={encodeURIComponent(map.id)}&duplicate=1"
							class="flex-1 rounded-lg border border-indigo-300 px-3 py-1.5 text-center text-sm text-indigo-800 hover:bg-indigo-50">Duplicate</a
						>
					</div>
					<p class="text-xs text-slate-600">Play starts a fresh session on this map. The program itself is yours alone.</p>
				</div>

				<div class="rounded-xl border border-indigo-200 bg-white p-4" data-testid="map-help">
					<h2 class="text-xs font-semibold uppercase tracking-widest text-indigo-900">This map</h2>
					<p class="mt-1 text-sm text-slate-700 lg:hidden">{map.description}</p>
					<dl class="mt-2 grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1 text-sm">
						<dt class="text-slate-600">Treats</dt>
						<dd class="font-medium text-indigo-950">{map.treats.length}</dd>
						<dt class="text-slate-600">Main tape</dt>
						<dd class="font-medium text-indigo-950">{map.mainTapeLength} slots</dd>
						<dt class="text-slate-600">Subroutines</dt>
						<dd class="font-medium text-indigo-950">
							{map.subTapeLengths.length ? `${map.subTapeLengths.join(' / ')} slots` : 'none'}
						</dd>
						<dt class="text-slate-600">Total slots</dt>
						<dd class="font-medium text-indigo-950">{totalSlots}</dd>
						<dt class="text-slate-600">Step limit</dt>
						<dd class="font-medium text-indigo-950">{map.maxSteps} steps</dd>
						<dt class="text-slate-600">Call depth</dt>
						<dd class="font-medium text-indigo-950">{map.maxCallDepth}</dd>
						{#if recursion}
							<dt class="text-slate-600">Recursion</dt>
							<dd class="font-medium text-indigo-950">
								<span class="rounded-full px-2 py-0.5 text-xs {recursion.classes}" title={recursion.title}>{recursion.label}</span>
							</dd>
						{/if}
					</dl>
					<a href="/motor/learn" class="mt-2 inline-block text-sm font-medium text-indigo-700 underline">All rules →</a>
				</div>

				<div class="lg:hidden"><TileLegend {map} /></div>
			</section>
		</div>
	{/if}
</div>
