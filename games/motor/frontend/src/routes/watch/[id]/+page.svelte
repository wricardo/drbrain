<script lang="ts">
	import { pageTitle } from '$lib/brand';
	import { onDestroy } from 'svelte';
	import { page } from '$app/stores';
	import { getContextClient } from '@urql/svelte';
	import { unwrap } from '$lib/api';
	import BoardPanel from '$lib/components/BoardPanel.svelte';
	import ConnectionBadge from '$lib/components/ConnectionBadge.svelte';
	import RunControls from '$lib/components/RunControls.svelte';
	import Stats from '$lib/components/Stats.svelte';
	import TileLegend from '$lib/components/TileLegend.svelte';
	import { describeError, type DescribedError } from '$lib/errors';
	import { PAUSE_MUTATION, RESET_MUTATION, RUN_MUTATION, STEP_MUTATION } from '$lib/queries';
	import { blockedCallNotice } from '$lib/recursion';
	import { ownerTokenFor, statusLine, watchTitle } from '$lib/sessions';
	import { initialSpeed, saveSpeed, savedSpeed } from '$lib/speed';
	import { createSessionStore } from '$lib/stores/session';
	import { createSessionSource } from '$lib/stores/source';
	import type { Session } from '$lib/types';

	const sessionId = $page.params.id ?? '';
	const client = getContextClient();
	// No owner token here: the spectator view never receives the program, it only drives playback.
	const store = createSessionStore(createSessionSource(client), sessionId);
	onDestroy(() => store.destroy());

	// Watching and driving playback (run, step, pause, reset) are open to everyone;
	// editing the program is not, and this page never receives it. Only a browser
	// that created the session can offer the Take over link.
	const ownerToken = ownerTokenFor(sessionId);

	const session = $derived($store.session);
	const status = $derived(session?.vm.status ?? 'READY');
	const playing = $derived(session?.playing ?? false);
	const terminal = $derived(status === 'WON' || status === 'LOST');
	const canReset = $derived(playing || (!!session && (terminal || session.vm.steps > 0)));
	const callNotice = $derived(session ? blockedCallNotice(session.lastEvent) : null);

	let speedMs = $state(500);
	let busy = $state(false);
	let actionError = $state<DescribedError | null>(null);
	let initialised = false;

	$effect(() => {
		const s = session;
		if (!s || initialised) return;
		initialised = true;
		speedMs = initialSpeed(s, savedSpeed());
	});

	/** Applies a playback response; a refusal (the run ended meanwhile) is reported inline. */
	async function act(fn: () => Promise<Session>) {
		busy = true;
		actionError = null;
		try {
			store.apply(await fn());
		} catch (e) {
			actionError = describeError(e);
		} finally {
			busy = false;
		}
	}

	const run = () =>
		act(async () => (await unwrap<{ run: Session }>(client.mutation(RUN_MUTATION, { sessionID: sessionId, speedMs }))).run);
	const pause = () =>
		act(async () => (await unwrap<{ pause: Session }>(client.mutation(PAUSE_MUTATION, { sessionID: sessionId }))).pause);
	const stepOnce = () =>
		act(async () => (await unwrap<{ step: Session }>(client.mutation(STEP_MUTATION, { sessionID: sessionId }))).step);
	const reset = () =>
		act(async () => (await unwrap<{ reset: Session }>(client.mutation(RESET_MUTATION, { sessionID: sessionId }))).reset);

	/** Remembers the chosen speed; while a run is playing it is restarted at the new speed (the server fixes the speed when a run starts). */
	function changeSpeed(ms: number) {
		saveSpeed(ms);
		if (!playing) return;
		void act(async () => {
			const paused = (await unwrap<{ pause: Session }>(client.mutation(PAUSE_MUTATION, { sessionID: sessionId }))).pause;
			if (paused.vm.status === 'WON' || paused.vm.status === 'LOST') return paused;
			return (await unwrap<{ run: Session }>(client.mutation(RUN_MUTATION, { sessionID: sessionId, speedMs: ms }))).run;
		});
	}
</script>

<svelte:head><title>{pageTitle(session ? watchTitle(session.map.name, { playing, status }) : 'Watch')}</title></svelte:head>

<div class="mx-auto max-w-6xl px-4 sm:px-6 py-6">
	{#if $store.loading}
		<p role="status" class="text-sm text-slate-600">Loading session…</p>
	{:else if !session}
		<div role="alert" class="rounded-lg border border-rose-300 bg-rose-50 p-4 text-sm text-rose-900">
			<strong>{$store.error?.title ?? 'Session unavailable'}.</strong>
			{$store.error?.message}
			<a href="/" class="ml-2 underline">Back to maps</a>
		</div>
	{:else}
		<div class="flex flex-wrap items-center justify-between gap-2">
			<div>
				<h1 class="text-2xl font-semibold text-indigo-950">Watching: {session.map.name}</h1>
				<p class="text-sm text-slate-600">{session.displayName || 'Anonymous'} · spectator view</p>
			</div>
			<ConnectionBadge state={$store.connection} />
		</div>
		{#if $store.error && $store.connection !== 'live'}
			<p role="status" class="mt-2 text-xs text-amber-800">{$store.error.title}: retrying…</p>
		{/if}
		{#if actionError}
			<p role="alert" class="mt-3 rounded-lg border border-rose-300 bg-rose-50 px-3 py-2 text-sm text-rose-900" data-testid="action-error">
				<strong>{actionError.title}</strong>{#if actionError.code}<span class="ml-1 font-mono text-xs">[{actionError.code}]</span>{/if}
				{#if actionError.message}— {actionError.message}{/if}
			</p>
		{/if}

		<div class="mt-4 grid gap-6 lg:grid-cols-[minmax(0,1fr)_minmax(0,28rem)]">
			<BoardPanel map={session.map} vm={session.vm} lastEvent={session.lastEvent} pinned />
			<section aria-label="Program" class="space-y-4">
				<p
					class="rounded-lg px-3 py-2 text-sm font-semibold {status === 'WON'
						? 'bg-emerald-50 text-emerald-900'
						: status === 'LOST'
							? 'bg-rose-50 text-rose-900'
							: playing
								? 'bg-indigo-600 text-white'
								: 'bg-indigo-50 text-indigo-900'}"
					role="status"
					aria-live="polite"
					data-testid="watch-status"
				>
					{statusLine(session)}
				</p>
				<Stats {session} />
				{#if callNotice}
					<p class="rounded-lg bg-amber-50 px-2 py-1.5 text-xs text-amber-900" role="status" data-testid="blocked-call">
						{callNotice}
					</p>
				{/if}
				<div class="rounded-xl border border-indigo-200 bg-white p-3 space-y-3" data-testid="watch-controls">
					<RunControls
						{playing}
						finished={terminal}
						{busy}
						{canReset}
						showBack={false}
						bind:speedMs
						onrun={run}
						onpause={pause}
						onstep={stepOnce}
						onspeed={changeSpeed}
						onreset={reset}
					/>
					<p class="text-xs text-slate-600">
						Anyone with this link can run, step, pause and reset the robot — the program itself stays private.
					</p>
				</div>
				<div class="lg:hidden"><TileLegend map={session.map} /></div>
				{#if ownerToken}
					<a href="/play/{session.id}" class="inline-block rounded-lg bg-indigo-600 px-4 py-2 text-sm font-semibold text-white hover:bg-indigo-700" data-testid="take-over">Take over</a>
				{/if}
			</section>
		</div>
	{/if}
</div>
