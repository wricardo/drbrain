<script lang="ts">
	import { pageTitle } from '$lib/motor/brand';
	import { onDestroy } from 'svelte';
	import { page } from '$app/stores';
	import { getContextClient } from '@urql/svelte';
	import { unwrap } from '$lib/motor/api';
	import BoardPanel from '$lib/motor/components/BoardPanel.svelte';
	import ConnectionBadge from '$lib/motor/components/ConnectionBadge.svelte';
	import RunControls from '$lib/motor/components/RunControls.svelte';
	import Stats from '$lib/motor/components/Stats.svelte';
	import Tape from '$lib/motor/components/Tape.svelte';
	import TileLegend from '$lib/motor/components/TileLegend.svelte';
	import { describeError, type DescribedError } from '$lib/motor/errors';
	import { PAUSE_MUTATION, RESET_MUTATION, RUN_MUTATION, STEP_MUTATION } from '$lib/motor/queries';
	import { blockedCallNotice } from '$lib/motor/recursion';
	import { ownerTokenFor, statusLine, watchTitle } from '$lib/motor/sessions';
	import { initialSpeed, saveSpeed, savedSpeed } from '$lib/motor/speed';
	import { createSessionStore } from '$lib/motor/stores/session';
	import { createSessionSource } from '$lib/motor/stores/source';
	import type { Session } from '$lib/motor/types';

	const sessionId = $page.params.id ?? '';
	const client = getContextClient();
	// This browser's owner token, if it created the session. A signed-in owner needs no
	// token (the login cookie proves it server-side); everyone else still gets a program-less
	// snapshot, so the watch link keeps driving playback without revealing a solution.
	const ownerToken = ownerTokenFor(sessionId);
	const store = createSessionStore(createSessionSource(client, ownerToken), sessionId);
	onDestroy(() => store.destroy());

	const session = $derived($store.session);
	const status = $derived(session?.vm.status ?? 'READY');
	const playing = $derived(session?.playing ?? false);
	const terminal = $derived(status === 'WON' || status === 'LOST');
	const canReset = $derived(playing || (!!session && (terminal || session.vm.steps > 0)));
	const callNotice = $derived(session ? blockedCallNotice(session.lastEvent) : null);
	// The server includes the program only for whoever may edit the run (this browser's owner
	// token, or the signed-in account that owns it), so its presence is the exact "this is my
	// run" signal: it drives both the read-only tape and the Take over link.
	const isOwner = $derived(!!session?.program);

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
			<a href="/motor" class="ml-2 underline">Back to maps</a>
		</div>
	{:else}
		<div class="flex flex-wrap items-center justify-between gap-2">
			<div>
				<h1 class="text-2xl font-semibold text-indigo-950">Watching: {session.map.name}</h1>
				<p class="text-sm text-slate-600">{session.displayName || 'Anonymous'} · {isOwner ? 'owner view' : 'spectator view'}</p>
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
				{#if session.program}
					<div class="rounded-xl border border-indigo-200 bg-white p-3 space-y-3" data-testid="watch-program">
						<h2 class="text-xs font-semibold uppercase tracking-widest text-indigo-900">Program</h2>
						<Tape
							program={session.program}
							disabled
							callStack={session.vm.callStack}
							lastEvent={session.lastEvent}
							{status}
						/>
					</div>
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
						{#if isOwner}
							You own this run, so its program is shown above; anyone with the link can still run, step, pause and reset it.
						{:else}
							Anyone with this link can run, step, pause and reset the robot — the program itself stays private.
						{/if}
					</p>
				</div>
				<div class="lg:hidden"><TileLegend map={session.map} /></div>
				{#if isOwner}
					<a href="/motor/play/{session.id}" class="inline-block rounded-lg bg-indigo-600 px-4 py-2 text-sm font-semibold text-white hover:bg-indigo-700" data-testid="take-over">Take over</a>
				{/if}
			</section>
		</div>
	{/if}
</div>
