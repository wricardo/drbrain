<script lang="ts">
	import Hero from '@drbrain/ui/components/Hero.svelte';
	import Button from '@drbrain/ui/components/Button.svelte';
	import EmptyState from '@drbrain/ui/components/EmptyState.svelte';
	import Skeleton from '@drbrain/ui/components/Skeleton.svelte';
	import { getContextClient, queryStore, gql } from '@urql/svelte';
	import { goto } from '$app/navigation';
	import { MAPS_QUERY, SESSIONS_QUERY } from '$lib/tesla/queries';
	import { mapLabel, prettyMapName, sortMaps, type MapLayout, type MapSummary } from '$lib/tesla/maps';
	import { fetchMapLayout } from '$lib/tesla/mapData';
	import { createGameSession } from '$lib/tesla/createSession';
	import { recentSessions, sessionStatus, type SessionSummary } from '$lib/tesla/sessions';
	import { absoluteTime, relativeTime } from '$lib/tesla/time';
	import type { StartSettings } from '$lib/tesla/startSettings';
	import MapCard from '$lib/tesla/components/MapCard.svelte';
	import StartSessionModal from '$lib/tesla/components/StartSessionModal.svelte';

	const PICK_COUNT = 6;

	const client = getContextClient();
	const mapsResult = queryStore({ client, query: gql(MAPS_QUERY) });
	const sessionsResult = queryStore({ client, query: gql(SESSIONS_QUERY), requestPolicy: 'network-only' });

	const maps = $derived<MapSummary[]>(sortMaps($mapsResult?.data?.maps ?? []));
	const pickMaps = $derived(maps.slice(0, PICK_COUNT));
	const firstMap = $derived(maps[0] ?? null);

	// undefined = loading, null = failed.
	let previews = $state<Record<string, MapLayout | null>>({});
	const requested = new Set<string>();
	$effect(() => {
		for (const m of pickMaps) {
			if (requested.has(m.mapId)) continue;
			requested.add(m.mapId);
			void fetchMapLayout(client, m.mapId).then((p) => (previews[m.mapId] = p));
		}
	});

	type RecentRow = SessionSummary & { battery: number; maxBattery: number };
	const recent = $derived<RecentRow[]>(
		recentSessions(
			($sessionsResult?.data?.sessions?.sessions ?? []).map(
				(s: { id: string; displayName: string | null; mapName: string; lastActionAt: string; gameState: RecentRow }) => ({
					id: s.id,
					displayName: s.displayName ?? null,
					mapName: s.mapName,
					lastActionAt: s.lastActionAt,
					score: s.gameState.score,
					totalMoves: s.gameState.totalMoves,
					victory: s.gameState.victory,
					gameOver: s.gameState.gameOver,
					battery: s.gameState.battery,
					maxBattery: s.gameState.maxBattery
				})
			)
		)
	);

	let sessionName = $state('');
	let createError = $state('');

	// Play opens the settings modal; the session is only created once the player
	// confirms fog/move-delay settings there.
	let pendingMap = $state<MapSummary | null>(null);
	let starting = $state(false);

	function play(map: MapSummary) {
		if (starting) return;
		createError = '';
		pendingMap = map;
	}

	function closeModal() {
		if (starting) return;
		pendingMap = null;
		createError = '';
	}

	async function startSession(settings: StartSettings) {
		const map = pendingMap;
		if (!map || starting) return;
		starting = true;
		createError = '';
		try {
			const id = await createGameSession(client, {
				mapID: map.mapId,
				name: sessionName,
				fogEnabled: settings.fogEnabled,
				fogRadius: settings.fogRadius,
				gridPassword: settings.gridPassword,
				moveDelayMs: settings.moveDelayMs
			});
			goto(`/tesla/watch/${id}`);
		} catch (err) {
			// Keep the modal open so the settings can be adjusted and retried.
			createError = err instanceof Error ? err.message : 'Could not create the session';
		} finally {
			starting = false;
		}
	}

	const statusText = { playing: 'Playing', won: '🏆 Won', lost: '💥 Lost' } as const;
</script>

<svelte:head>
	<title>Tesla Road Trip · drbrain</title>
</svelte:head>

<div class="db-container game-landing">
	<Hero
		label="Tesla Road Trip"
		title="Visit every park."
		accent="Make the charge last."
		description="Plan a route around chargers, water and buildings. Drive yourself or hand the wheel to an AI agent and watch it live."
		image="/art/tesla.webp"
		wideArt
	>
		<Button href="#pick-a-map">Choose a map <span aria-hidden="true">↓</span></Button>
		<Button href="/tesla/learn" variant="secondary">How it works</Button>
	</Hero>
</div>

<!-- Pick a map -->
<section id="pick-a-map" class="db-container py-8 sm:py-10 scroll-mt-6">
	<div class="flex flex-wrap items-end justify-between gap-4 mb-4">
		<div>
			<h2 class="text-2xl font-bold tracking-tight">Pick a map</h2>
			<p class="text-sm text-gray-500 mt-0.5">Your drafts first, then smallest maps. Press Play to choose fog and move-delay settings.</p>
		</div>
		<div class="w-full sm:w-72">
			<label for="session-name" class="block text-xs font-semibold text-[#393c41] mb-1.5">Name <span class="font-normal text-gray-400">(optional)</span></label>
			<input
				id="session-name"
				type="text"
				bind:value={sessionName}
				placeholder="e.g. Claude's first run"
				class="db-input"
			/>
		</div>
	</div>


	{#if $mapsResult.fetching && maps.length === 0}
		<Skeleton label="Loading maps…" height="12rem" />
	{:else if $mapsResult.error}
		<p role="alert" class="rounded-2xl border border-red-200 bg-red-50 px-5 py-4 text-sm text-red-700">Could not load maps. {$mapsResult.error.message}</p>
	{:else if maps.length === 0}
		<EmptyState title="No routes available" description="Create a map to plan your own road trip.">
			<Button variant="secondary" href="/tesla/editor">Open the map editor</Button>
		</EmptyState>
	{:else}
		<div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-5">
			{#each pickMaps as map (map.mapId)}
				<MapCard
					{map}
					preview={previews[map.mapId] ?? undefined}
					previewError={previews[map.mapId] === null}
					busy={starting && pendingMap?.mapId === map.mapId}
					onplay={play}
				/>
			{/each}
		</div>
		<div class="mt-6 text-center">
			<a href="/tesla/maps" class="text-sm font-medium text-[#393c41] hover:underline">Browse all {maps.length} maps →</a>
		</div>
	{/if}
</section>

<!-- Recent sessions -->
<section class="db-container pb-12">
	<div class="flex items-end justify-between gap-4 mb-4">
		<h2 class="text-2xl font-bold tracking-tight">Recent sessions</h2>
		<a href="/tesla/lobby" class="text-sm text-gray-600 hover:text-gray-900">All sessions →</a>
	</div>
	{#if recent.length === 0}
		<div class="rounded-2xl border border-[#e8e8e8] bg-white px-5 py-8 text-center text-sm text-gray-500">
			No one is driving yet. <button type="button" onclick={() => firstMap && play(firstMap)} class="font-medium text-[#393c41] underline">Start a session</button>
		</div>
	{:else}
		<ul class="db-panel divide-y divide-gray-100">
			{#each recent as s (s.id)}
				{@const st = sessionStatus(s)}
				<li>
					<a href="/tesla/watch/{s.id}" class="flex flex-wrap items-center gap-x-4 gap-y-1 px-5 py-3 hover:bg-gray-50 transition-colors">
						<span class="min-w-0 flex-1 truncate text-sm text-[#393c41]">{s.displayName ?? 'Unnamed session'} <span class="text-gray-400">· {mapLabel(s.mapName, maps)}</span></span>
						<span class="text-xs text-gray-500 tabular-nums">🌳 {s.score} parks · {s.totalMoves} moves</span>
						<span class="text-xs w-20 {st === 'won' ? 'text-green-700' : st === 'lost' ? 'text-red-700' : 'text-gray-600'}">{statusText[st]}</span>
						<span class="text-xs text-gray-500 w-16 text-right" title={absoluteTime(s.lastActionAt)}>{relativeTime(s.lastActionAt)}</span>
					</a>
				</li>
			{/each}
		</ul>
	{/if}
</section>

{#if pendingMap}
	<StartSessionModal map={pendingMap} {starting} error={createError} onstart={startSession} onclose={closeModal} />
{/if}
