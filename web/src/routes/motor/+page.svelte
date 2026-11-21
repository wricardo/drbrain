<script lang="ts">
	import Hero from '@drbrain/ui/components/Hero.svelte';
	import Button from '@drbrain/ui/components/Button.svelte';
	import EmptyState from '@drbrain/ui/components/EmptyState.svelte';
	import Skeleton from '@drbrain/ui/components/Skeleton.svelte';
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { getContextClient } from '@urql/svelte';
	import { unwrap } from '@drbrain/ui/graphql';
	import MapCard from '$lib/motor/components/MapCard.svelte';
	import { sortMaps } from '$lib/motor/maps';
	import { describeError, type DescribedError } from '@drbrain/ui/errors';
	import { CREATE_SESSION_MUTATION, MAPS_QUERY, SESSION_QUERY, SESSIONS_QUERY } from '$lib/motor/queries';
	import { forgetSession, isFinished, lastSessionId, ownsSession, progressText, recentStarted, rememberSession, statusText } from '$lib/motor/sessions';
	import { account } from '$lib/account/stores/account';
	import { absoluteTime, relativeTime } from '@drbrain/ui/time';
	import type { GameMap, Session, SessionSummary } from '$lib/motor/types';

	const client = getContextClient();

	let maps = $state<GameMap[]>([]);
	let sessions = $state<SessionSummary[]>([]);
	let resume = $state<Session | null>(null);
	let loading = $state(true);
	let creating = $state(false);
	let error = $state<DescribedError | null>(null);
	const starter = $derived(maps[0] ?? null);
	const mapById = $derived(new Map(maps.map((m) => [m.id, m])));
	/** Home shows only the first few maps; the rest live on /maps. */
	const HOME_MAPS = 3;

	onMount(async () => {
		try {
			const [m, s] = await Promise.all([
				unwrap<{ maps: GameMap[] }>(client.query(MAPS_QUERY, {})),
				unwrap<{ sessions: SessionSummary[] }>(client.query(SESSIONS_QUERY, { limit: 50 }))
			]);
			maps = sortMaps(m.maps);
			sessions = recentStarted(s.sessions, 8);
		} catch (e) {
			error = describeError(e);
		} finally {
			loading = false;
		}
		await loadResume();
	});

	/** Loads this browser's last created session for the Continue card; forgets it once finished or gone. */
	async function loadResume() {
		const id = lastSessionId();
		if (!id) return;
		try {
			const data = await unwrap<{ session: Session | null }>(client.query(SESSION_QUERY, { id }, { requestPolicy: 'network-only' }));
			if (data.session && !isFinished(data.session)) resume = data.session;
			else forgetSession();
		} catch {
			// not found or offline: no Continue card
		}
	}

	async function play(map: GameMap) {
		creating = true;
		error = null;
		try {
			const data = await unwrap<{ createSession: Session }>(
				client.mutation(CREATE_SESSION_MUTATION, { mapID: map.id })
			);
			rememberSession(data.createSession.id, data.createSession.ownerToken);
			await goto(`/motor/play/${data.createSession.id}`);
		} catch (e) {
			error = describeError(e);
		} finally {
			creating = false;
		}
	}
</script>

<svelte:head><title>Motor Programming · drbrain</title></svelte:head>

<div class="db-container game-landing">
	<Hero
		label="Motor Programming"
		title="Program the robot."
		accent="Collect every treat."
		description="Build a tiny program of moves, turns and subroutines. Spot the patterns and make every instruction count."
		image="/art/motor.webp"
		wideArt
	>
		<Button disabled={!starter || creating} onclick={() => starter && play(starter)} data-testid="play-easiest">
			{creating ? 'Starting…' : starter ? `Play ${starter.name}` : loading ? 'Loading maps…' : 'Play unavailable'}
			<span aria-hidden="true">→</span>
		</Button>
		<Button href="/motor/maps" variant="secondary">Browse maps</Button>
	</Hero>

	{#if resume}
		<aside class="db-panel game-resume flex flex-wrap items-center justify-between gap-3" aria-label="Continue your game" data-testid="continue-card">
			<div>
				<p class="db-kicker">Pick up where you left off</p>
				<p class="mt-1 font-semibold">{resume.map.name} <span class="font-normal text-slate-600">· {resume.vm.steps} / {resume.map.maxSteps} steps</span></p>
			</div>
			<Button href="/motor/play/{resume.id}" variant="secondary">Continue <span aria-hidden="true">→</span></Button>
		</aside>
	{/if}
</div>

<div class="db-container space-y-10 py-8 sm:py-10">

	<section id="maps" class="scroll-mt-6">
		<div class="flex flex-wrap items-baseline justify-between gap-2">
			<h2 class="text-2xl font-bold tracking-tight">Pick a map</h2>
			{#if maps.length > HOME_MAPS}
				<a href="/motor/maps" class="text-sm font-medium text-indigo-700 underline" data-testid="browse-all">Browse all {maps.length} maps →</a>
			{/if}
		</div>
		<p class="mt-1 text-sm text-slate-700">Start with the easiest maps. New here? Read <a href="/motor/learn" class="underline text-indigo-700">how it works</a>.</p>
		<div class="mt-4 max-w-sm">
			{#if $account.me}
				<p class="text-sm text-slate-700" data-testid="playing-as">
					Playing as <strong class="text-indigo-950">{$account.me.displayName || $account.me.handle}</strong>
					<span class="text-slate-500">@{ $account.me.handle }</span>. Your runs are saved to your profile and appear
					under your handle; change the name in <a href="/settings" class="text-indigo-700 underline">Settings</a>.
				</p>
			{:else}
				<p class="text-sm text-slate-700" data-testid="playing-as">
					Playing as a guest: your runs appear as <strong class="text-indigo-950">Guest</strong> on the
					leaderboard. <a href="/settings" class="text-indigo-700 underline">Sign in</a> to appear under your handle.
				</p>
			{/if}
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
					<Button href="/motor/editor" variant="secondary">Open the map editor</Button>
				</EmptyState>
			</div>
		{:else}
			<div class="mt-6 grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
				{#each maps.slice(0, HOME_MAPS) as map (map.id)}
					<MapCard {map} busy={creating} onplay={play} />
				{/each}
			</div>
		{/if}
	</section>

	<section aria-label="What you will do" class="grid gap-4 sm:grid-cols-3">
		<div class="db-panel p-5">
			<h2 class="mt-2 font-medium text-indigo-950">Program, don't drive</h2>
			<p class="mt-1 text-sm leading-relaxed text-slate-600">Drop instructions into the slots, press Run and watch the robot execute them one step at a time.</p>
		</div>
		<div class="db-panel p-5">
			<h2 class="mt-2 font-medium text-indigo-950">Find the pattern</h2>
			<p class="mt-1 text-sm leading-relaxed text-slate-600">Tapes are tiny. Package repeated rows, turns and loops into subroutines — and nest them — to fit the route.</p>
		</div>
		<div class="db-panel p-5">
			<h2 class="mt-2 font-medium text-indigo-950">Try it with an AI</h2>
			<p class="mt-1 text-sm leading-relaxed text-slate-600">Every session has a “Play with an AI” prompt you can paste into a chat. <a href="/motor/learn#ai" class="underline text-indigo-700">How agents play</a></p>
		</div>
	</section>

	<section aria-labelledby="recent-h">
		<div class="flex items-baseline justify-between gap-2">
			<h2 id="recent-h" class="text-xl font-semibold text-indigo-950">Recent sessions</h2>
			<a href="/motor/sessions" class="text-sm text-indigo-700 underline">View all sessions</a>
		</div>
		{#if sessions.length === 0}
			<p class="mt-2 text-sm text-slate-600">
				Nobody has played yet.
				<button
					type="button"
					class="ml-2 rounded-full bg-indigo-600 px-4 py-1.5 text-sm font-semibold text-white hover:bg-indigo-700 disabled:opacity-50"
					disabled={!starter || creating}
					onclick={() => starter && play(starter)}>Play the first map</button
				>
			</p>
		{:else}
			<ul class="mt-3 divide-y divide-indigo-100 rounded-xl border border-indigo-200 bg-white">
				{#each sessions as s (s.id)}
					<li class="flex flex-wrap items-center justify-between gap-2 px-4 py-3 text-sm">
						<div>
							<span class="font-medium">{s.map.name}</span>
							<span class="ml-2 rounded-full bg-indigo-50 px-2 py-0.5 text-xs text-indigo-800">{statusText(s)}</span>
						</div>
						<div class="flex items-center gap-4 text-xs text-slate-600">
							<span>{progressText(s, mapById.get(s.mapId))}</span>
							<span title={absoluteTime(s.lastActionAt)}>{relativeTime(s.lastActionAt)}</span>
							{#if !isFinished(s) && ownsSession(s.id)}
								<a class="font-semibold text-indigo-700 underline" href="/motor/play/{s.id}">Play</a>
							{/if}
							<a class="text-indigo-700 underline" href="/motor/watch/{s.id}">Watch</a>
						</div>
					</li>
				{/each}
			</ul>
		{/if}
	</section>
</div>
