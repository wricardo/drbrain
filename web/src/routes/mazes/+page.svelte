<script lang="ts">
	import Hero from '@drbrain/ui/components/Hero.svelte';
	import Button from '@drbrain/ui/components/Button.svelte';
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { getContextClient } from '@urql/svelte';
	import { unwrap } from '@drbrain/ui/graphql';
	import { isDraft, sortMapsByDifficulty } from '$lib/mazes/maps';
	import { describeError, type DescribedError } from '$lib/mazes/errors';
	import { MAPS_QUERY } from '$lib/mazes/queries';
	import { savedName, startSession } from '$lib/mazes/start';
	import type { GameMap } from '$lib/mazes/types';

	const client = getContextClient();

	let maps = $state<GameMap[]>([]);
	let loading = $state(true);
	let creating = $state(false);
	let error = $state<DescribedError | null>(null);
	const starter = $derived(maps.find((m) => !isDraft(m)) ?? null);
	let displayName = $state(savedName());

	onMount(async () => {
		try {
			const m = await unwrap<{ maps: GameMap[] }>(client.query(MAPS_QUERY, {}));
			maps = sortMapsByDifficulty(m.maps);
		} catch (e) {
			error = describeError(e);
		} finally {
			loading = false;
		}
	});

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
</div>

<div class="db-container space-y-10 py-8 sm:py-10">
	{#if error}
		<p role="alert" class="rounded-lg border border-rose-300 bg-rose-50 px-3 py-2 text-sm text-rose-900">
			<strong>{error.title}.</strong> {error.message}
		</p>
	{/if}

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

</div>
