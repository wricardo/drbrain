<script lang="ts">
	import Hero from '@drbrain/ui/components/Hero.svelte';
	import Button from '@drbrain/ui/components/Button.svelte';
	import Skeleton from '@drbrain/ui/components/Skeleton.svelte';
	import { onDestroy, onMount } from 'svelte';
	import { getContextClient } from '@urql/svelte';
	import { createSessionStore, type SessionState, type SessionStore } from '@drbrain/ui/session';
	import { describeError, type DescribedError } from '@drbrain/ui/errors';
	import MusicPuzzle from '$lib/music/components/MusicPuzzle.svelte';
	import { disposeSynthPlayer, getSynthPlayer, type SynthPlayer } from '$lib/music/audio';
	import {
		createSession,
		createSessionSource,
		fetchExcerpts,
		resetArrangement,
		switchMeasures,
		transformMeasure
	} from '$lib/music/graphql';
	import { playableEvents, targetEvents } from '$lib/music/score';
	import type { Difficulty, Excerpt, Session, SessionUpdate, Slot } from '$lib/music/types';

	const client = getContextClient();

	const DIFFICULTIES: { id: Difficulty; label: string; percent: number; hint: string }[] = [
		{ id: 'NOVICE', label: 'Novice', percent: 5, hint: 'Reorder only' },
		{ id: 'EXPERT', label: 'Expert', percent: 10, hint: 'Reorder + undo H flips' },
		{ id: 'GENIUS', label: 'Genius', percent: 15, hint: 'Reorder + undo H and V flips' }
	];

	let excerpts = $state<Excerpt[]>([]);
	let loading = $state(true);
	let loadError = $state<DescribedError | null>(null);
	let difficulty = $state<Difficulty>('NOVICE');
	let pickedExcerpt = $state<string | null>(null);
	let sessionState = $state<SessionState<Session> | null>(null);
	let busy = $state(false);
	let actionError = $state<DescribedError | null>(null);
	let playing = $state(false);

	let store: SessionStore<Session, SessionUpdate> | null = null;
	let unsubscribe: (() => void) | null = null;
	let player: SynthPlayer | null = null;

	const session = $derived(sessionState?.session ?? null);
	const excerpt = $derived(session?.excerpt ?? excerpts.find((e) => e.id === pickedExcerpt) ?? excerpts[0] ?? null);
	const activeExcerptID = $derived(session?.excerpt.id ?? pickedExcerpt ?? excerpts[0]?.id ?? null);

	onMount(() => {
		void load();
	});

	onDestroy(() => {
		stopAudio();
		detach();
		disposeSynthPlayer();
	});

	async function load() {
		try {
			excerpts = await fetchExcerpts(client);
			pickedExcerpt = excerpts[0]?.id ?? null;
		} catch (e) {
			loadError = describeError(e);
		} finally {
			loading = false;
		}
	}

	function detach() {
		unsubscribe?.();
		unsubscribe = null;
		store?.destroy();
		store = null;
	}

	function attach(id: string) {
		detach();
		const created = createSessionStore(createSessionSource(client), id);
		store = created;
		unsubscribe = created.subscribe((state) => {
			sessionState = state;
		});
	}

	async function start(excerptID: string | null, level: Difficulty) {
		if (!excerptID) return;
		busy = true;
		actionError = null;
		stopAudio();
		try {
			const created = await createSession(client, excerptID, level);
			pickedExcerpt = excerptID;
			difficulty = level;
			attach(created.id);
			store?.apply(created);
		} catch (e) {
			actionError = describeError(e);
		} finally {
			busy = false;
		}
	}

	function stopAudio() {
		player?.stop();
		playing = false;
	}

	function toggleListen() {
		if (playing) {
			stopAudio();
			return;
		}
		if (!excerpt) return;
		try {
			player = getSynthPlayer();
			player.play(targetEvents(excerpt), excerpt.tempoBPM);
			playing = true;
		} catch (e) {
			actionError = describeError(e);
		}
	}

	function handlePlay(positions: number[]) {
		const live = session;
		if (!live) return;
		try {
			player = getSynthPlayer();
			player.play(playableEvents(live, positions), live.excerpt.tempoBPM);
			playing = true;
		} catch (e) {
			actionError = describeError(e);
		}
	}

	async function run(mutation: () => Promise<Session>) {
		busy = true;
		actionError = null;
		try {
			const snapshot = await mutation();
			store?.apply(snapshot);
		} catch (e) {
			actionError = describeError(e);
		} finally {
			busy = false;
		}
	}

	function handleSwitch(a: number, b: number) {
		const live = session;
		if (!live) return;
		void run(() => switchMeasures(client, live.id, a, b));
	}

	function handleTransform(positions: number[], axis: 'h' | 'v') {
		const live = session;
		if (!live) return;
		const targets = positions
			.map((position) => live.slots.find((slot) => slot.position === position))
			.filter((slot): slot is Slot => slot !== undefined);
		if (!targets.length) return;
		void run(async () => {
			let latest = live;
			for (const slot of targets) {
				latest = await transformMeasure(
					client,
					live.id,
					slot.position,
					axis === 'h' ? !slot.flipH : slot.flipH,
					axis === 'v' ? !slot.flipV : slot.flipV
				);
			}
			return latest;
		});
	}

	function handleReset() {
		const live = session;
		if (!live) return;
		stopAudio();
		void run(() => resetArrangement(client, live.id));
	}
</script>

<svelte:head><title>{session ? session.excerpt.title + " · Music Region" : "Music Region · drbrain"}</title></svelte:head>

<div class="db-container game-landing pb-10">
	{#if session}
		<header class="db-panel game-run-title">
			<div>
				<p class="db-kicker">Music Region</p>
				<h1 class="mt-1 text-2xl sm:text-3xl">{session.excerpt.title}</h1>
				<p class="mt-1 text-sm text-stone-600">
					{session.excerpt.composer} · {session.excerpt.measures.length} measures · {session.excerpt.timeSig} · ♩={session.excerpt.tempoBPM}
				</p>
			</div>
			<Button variant="secondary" onclick={toggleListen} disabled={!excerpt} data-testid="listen">
				{playing ? '■ Stop' : '▶ Listen to the original'}
			</Button>
		</header>
	{:else}
		<Hero
			label="Music Region"
			title="Rebuild the score."
			accent="Hear it come together."
			description="Put the measures of a scrambled piece in order, then listen to the result. Higher difficulties let you reverse or invert measures."
			image="/art/music.webp"
			wideArt
		>
			<Button href="#excerpts">Choose a piece <span aria-hidden="true">↓</span></Button>
			<Button variant="secondary" onclick={toggleListen} disabled={!excerpt} data-testid="listen">
				{playing ? '■ Stop' : '▶ Listen to the original'}
			</Button>
		</Hero>
	{/if}

	{#if loadError}
		<p class="mt-6 rounded-xl border border-red-300 bg-red-50 px-4 py-3 text-sm text-red-900" role="alert">
			{loadError.title}: {loadError.message || 'Could not load the excerpt catalogue.'}
		</p>
	{:else if actionError}
		<p class="mt-6 rounded-xl border border-red-300 bg-red-50 px-4 py-3 text-sm text-red-900" role="alert" data-testid="error">
			{actionError.title}: {actionError.message}
		</p>
	{/if}

	<div class="mt-6 grid grid-cols-[minmax(0,1fr)] gap-5 lg:grid-cols-[minmax(0,1fr)_330px] lg:items-start">
		<div class={!session ? "order-2 lg:order-none" : ""}>
			{#if session}
				<MusicPuzzle
					{session}
					{playing}
					{busy}
					onplay={handlePlay}
					onstop={stopAudio}
					onswitch={handleSwitch}
					ontransform={handleTransform}
					onreset={handleReset}
				/>
			{:else}
				{#if loading}
					<Skeleton label="Loading excerpts…" />
				{:else if excerpts.length}
					<div class="db-panel score-help" data-testid="empty-state">
						<h2>How to rebuild a score</h2>
						<ol>
							<li>Choose a piece and difficulty.</li>
							<li>Tap two measures, then press <strong>Switch</strong> to swap them.</li>
							<li>On Expert or Genius, use the flip controls to restore reversed or inverted notes.</li>
						</ol>
					</div>
				{/if}
			{/if}
		</div>

		<aside class="space-y-4" aria-label="Excerpts and difficulty" id="excerpts">
			<section class="db-panel p-4">
				<h2 class="text-xs font-bold uppercase tracking-widest text-stone-500">Excerpts</h2>
				{#if loading}
					<p class="mt-3 text-sm text-stone-500">Loading…</p>
				{:else if !excerpts.length}
					<p class="mt-3 text-sm text-stone-500">No excerpts are installed.</p>
				{:else}
					<ul class="mt-3 space-y-1.5">
						{#each excerpts as piece (piece.id)}
							{@const active = piece.id === activeExcerptID}
							<li>
								<button
									type="button"
									aria-pressed={active}
									onclick={() => start(piece.id, difficulty)}
									disabled={busy}
									class="min-h-11 w-full rounded-lg border px-3 py-2 text-left transition-colors {active
										? 'border-amber-600 bg-amber-50'
										: 'border-transparent hover:border-amber-300 hover:bg-amber-50/60'} disabled:opacity-50"
								>
									<span class="block text-sm font-medium text-stone-900">{piece.title}</span>
									<span class="block text-xs text-stone-500">{piece.composer} · {piece.measures.length} bars</span>
								</button>
							</li>
						{/each}
					</ul>
				{/if}
			</section>

			<section class="db-panel p-4">
				<h2 class="text-xs font-bold uppercase tracking-widest text-stone-500">Difficulty</h2>
				<div class="mt-3 grid gap-1.5" role="group" aria-label="Difficulty">
					{#each DIFFICULTIES as level (level.id)}
						<button
							type="button"
							aria-pressed={difficulty === level.id}
							onclick={() => start(activeExcerptID, level.id)}
							disabled={busy || !activeExcerptID}
							data-testid="difficulty-{level.id.toLowerCase()}"
							class="flex min-h-11 items-center justify-between rounded-lg border px-3 py-2 text-sm transition-colors {difficulty ===
							level.id
								? 'border-amber-600 bg-amber-50 font-semibold text-amber-950'
								: 'border-stone-200 text-stone-700 hover:border-amber-300 hover:bg-amber-50/60'} disabled:opacity-50"
						>
							<span class="text-left">
								<span class="block">{level.label}</span>
								<span class="block text-xs font-normal text-stone-500" data-testid="difficulty-hint-{level.id.toLowerCase()}">{level.hint}</span>
							</span>
							<span class="text-xs text-stone-500">+{level.percent}% / win</span>
						</button>
					{/each}
				</div>
				<p class="mt-3 text-xs leading-relaxed text-stone-500">
					Changing difficulty starts a fresh session.
				</p>
			</section>
		</aside>
	</div>
</div>


<style>
	.score-help { padding: clamp(1.15rem, 3vw, 2rem); }
	.score-help h2 { color: var(--db-ink); font-weight: 710; font-size: 1.2rem; letter-spacing: -.03em; }
	.score-help ol { list-style-type: decimal; padding-inline-start: 1.25rem; margin-top: .8rem; color: var(--db-muted); line-height: 1.65; font-size: .9rem; }
	#excerpts { scroll-margin-top: 1rem; }
</style>
