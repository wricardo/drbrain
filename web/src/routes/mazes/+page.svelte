<script lang="ts">
	import Hero from '@drbrain/ui/components/Hero.svelte';
	import Button from '@drbrain/ui/components/Button.svelte';
	import EmptyState from '@drbrain/ui/components/EmptyState.svelte';
	import Skeleton from '@drbrain/ui/components/Skeleton.svelte';
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { getContextClient } from '@urql/svelte';
	import { unwrap } from '@drbrain/ui/graphql';
	import MapCard from '$lib/mazes/components/MapCard.svelte';
	import { sortMapsByDifficulty } from '$lib/mazes/maps';
	import { describeError, type DescribedError } from '$lib/mazes/errors';
	import { MAPS_QUERY, SESSIONS_QUERY } from '$lib/mazes/queries';
	import { createSessionSource } from '$lib/mazes/stores/source';
	import { forgetSession, isFinished, recalledSession, recentStarted, statusText } from '$lib/mazes/sessions';
	import { savedName, startSession } from '$lib/mazes/start';
	import { absoluteTime, relativeTime } from '$lib/mazes/time';
	import type { GameMap, Session, SessionSummary } from '$lib/mazes/types';

	/** Maps shown on the home page; the rest are one click away on /maps. */
	const HOME_MAPS = 3;

	const client = getContextClient();

	let maps = $state<GameMap[]>([]);
	let sessions = $state<SessionSummary[]>([]);
	let resume = $state<Session | null>(null);
	let loading = $state(true);
	let creating = $state(false);
	let error = $state<DescribedError | null>(null);
	const starter = $derived(maps[0] ?? null);
	const recent = $derived(recentStarted(sessions));
	let displayName = $state(savedName());

	onMount(async () => {
		void loadResume();
		try {
			const [m, s] = await Promise.all([
				unwrap<{ maps: GameMap[] }>(client.query(MAPS_QUERY, {})),
				unwrap<{ sessions: SessionSummary[] }>(client.query(SESSIONS_QUERY, { limit: 40 }, { requestPolicy: 'network-only' }))
			]);
			maps = sortMapsByDifficulty(m.maps);
			sessions = s.sessions;
		} catch (e) {
			error = describeError(e);
		} finally {
			loading = false;
		}
	});

	/** The "Continue" card: the last session this browser created, while it is unfinished. */
	async function loadResume() {
		const id = recalledSession();
		if (!id) return;
		try {
			const s = await createSessionSource(client).fetch(id);
			if (s && !isFinished(s)) resume = s;
			else forgetSession();
		} catch (e) {
			// Gone from the server: forget it. Anything else (offline): no card this time.
			if (describeError(e).code === 'NOT_FOUND') forgetSession();
		}
	}

	async function play(map: GameMap) {
		creating = true;
		error = null;
		try {
			await goto(`/mazes/play/${await startSession(client, map.id, displayName)}`);
		} catch (e) {
			error = describeError(e);
		} finally {
			creating = false;
		}
	}
</script>

<svelte:head><title>Mazes · drbrain</title></svelte:head>

<div class="db-container game-landing">
	<Hero
		label="Mazes"
		title="Climb the tower."
		accent="Mind the portals."
		description="Guide the miner from the bottom of a fog-wrapped tower to the finish. Stairs and portals connect its floors."
		image="/art/mazes.webp"
		wideArt
	>
		<Button disabled={!starter || creating} onclick={() => starter && play(starter)} data-testid="play-easiest">
			{creating ? 'Starting…' : starter ? `Play ${starter.name}` : loading ? 'Loading maps…' : 'Play unavailable'}
			<span aria-hidden="true">→</span>
		</Button>
		<Button href="/mazes/maps" variant="secondary">Browse maps</Button>
	</Hero>

	{#if resume}
		<aside class="db-panel game-resume flex flex-wrap items-center justify-between gap-3" aria-label="Continue your game" data-testid="continue">
			<div>
				<p class="db-kicker">Pick up where you left off</p>
				<p class="mt-1 font-semibold">{resume.map.name} <span class="font-normal text-slate-600">· {resume.state.moves} / {resume.map.maxMoves} moves</span></p>
			</div>
			<Button href="/mazes/play/{resume.id}" variant="secondary">Continue <span aria-hidden="true">→</span></Button>
		</aside>
	{/if}
</div>

<div class="db-container space-y-10 py-8 sm:py-10">

	<section id="maps" class="scroll-mt-6">
		<div class="flex flex-wrap items-baseline justify-between gap-2">
			<h2 class="text-2xl font-bold tracking-tight">Pick a map</h2>
			{#if maps.length > 0}
				<a href="/mazes/maps" class="text-sm font-medium text-indigo-700 underline" data-testid="browse-all">Browse all {maps.length} maps →</a>
			{/if}
		</div>
		<p class="mt-1 text-sm text-slate-700">The easiest maps first. New here? Read <a href="/mazes/learn" class="underline text-indigo-700">how it works</a>.</p>
		<div class="mt-4 max-w-sm">
			<label for="display-name" class="block text-sm font-medium text-indigo-900">Your name (optional)</label>
			<input
				id="display-name"
				type="text"
				maxlength="40"
				bind:value={displayName}
				placeholder="Anonymous"
				class="db-input mt-1"
			/>
		</div>

		{#if error}
			<p role="alert" class="mt-4 rounded-lg border border-rose-300 bg-rose-50 px-3 py-2 text-sm text-rose-900">
				<strong>{error.title}.</strong> {error.message}
			</p>
		{/if}

		{#if loading}
			<div class="mt-6"><Skeleton label="Loading maps…" /></div>
		{:else if maps.length === 0 && !error}
			<div class="mt-6">
				<EmptyState title="No maps available" description="Build a map to start a new challenge.">
					<Button href="/mazes/editor" variant="secondary">Open the map editor</Button>
				</EmptyState>
			</div>
		{:else}
			<div class="mt-6 grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
				{#each maps.slice(0, HOME_MAPS) as map (map.id)}
					<MapCard {map} busy={creating} onplay={play} />
				{/each}
			</div>
			{#if maps.length > HOME_MAPS}
				<p class="mt-4 text-center">
					<a href="/mazes/maps" class="inline-block rounded-full border border-indigo-300 bg-white px-5 py-2 text-sm font-semibold text-indigo-900 hover:bg-indigo-50"
						>Browse all {maps.length} maps →</a
					>
				</p>
			{/if}
		{/if}
	</section>

	<section aria-label="What you will do" class="grid gap-4 sm:grid-cols-3">
		<div class="db-panel p-5">
			<h2 class="mt-2 font-medium text-indigo-950">One tile at a time</h2>
			<p class="mt-1 text-sm leading-relaxed text-slate-600">Move with the arrow keys, WASD, the d-pad or by clicking a neighbouring tile. Bumping into the void is free.</p>
		</div>
		<div class="db-panel p-5">
			<h2 class="mt-2 font-medium text-indigo-950">Stairs and portals</h2>
			<p class="mt-1 text-sm leading-relaxed text-slate-600">Stairs change level, portal pairs jump across the tower. Only your own floor is visible; the tower map shows which floor you are on and where FINISH is — always on top.</p>
		</div>
		<div class="db-panel p-5">
			<h2 class="mt-2 font-medium text-indigo-950">Try it with an AI</h2>
			<p class="mt-1 text-sm leading-relaxed text-slate-600">Every session has a “Play with an AI” prompt you can paste into a chat to have an agent steer the miner. <a href="/mazes/learn#ai" class="underline text-indigo-700">How agents play</a></p>
		</div>
	</section>

	<section aria-labelledby="recent-h">
		<div class="flex items-baseline justify-between gap-2">
			<h2 id="recent-h" class="text-xl font-semibold text-indigo-950">Recent sessions</h2>
			<a href="/mazes/sessions" class="text-sm text-indigo-700 underline">View all sessions</a>
		</div>
		{#if loading}
			<p class="mt-2 text-sm text-slate-600" role="status">Loading sessions…</p>
		{:else if recent.length === 0}
			<p class="mt-3 rounded-xl border border-indigo-200 bg-white px-4 py-6 text-sm text-slate-600">
				Nobody has made a move yet.
				<button
					type="button"
					class="ml-2 rounded-lg bg-indigo-600 px-3 py-1.5 font-semibold text-white hover:bg-indigo-700 disabled:opacity-50"
					disabled={!starter || creating}
					onclick={() => starter && play(starter)}>Be the first</button
				>
			</p>
		{:else}
			<ul class="mt-3 divide-y divide-indigo-100 rounded-xl border border-indigo-200 bg-white">
				{#each recent as s (s.id)}
					{@const st = statusText(s)}
					<li class="flex flex-wrap items-center justify-between gap-2 px-4 py-3 text-sm">
						<div>
							<span class="font-medium">{s.displayName || 'Anonymous'}</span>
							<span class="text-slate-600"> · {s.map.name}</span>
							<span class="ml-2 rounded-full bg-indigo-50 px-2 py-0.5 text-xs text-indigo-800">{st}</span>
						</div>
						<div class="flex items-center gap-4 text-xs text-slate-600">
							<span class="tabular-nums">{s.state.moves} / {s.map.maxMoves} moves{s.map.parMoves !== null ? ` · par ${s.map.parMoves}` : ''}</span>
							<span title={absoluteTime(s.lastActionAt)}>{relativeTime(s.lastActionAt)}</span>
							{#if !isFinished(s)}<a class="font-semibold text-indigo-700 underline" href="/mazes/play/{s.id}">Play</a>{/if}
							<a class="text-indigo-700 underline" href="/mazes/watch/{s.id}">Watch</a>
						</div>
					</li>
				{/each}
			</ul>
		{/if}
	</section>
</div>
