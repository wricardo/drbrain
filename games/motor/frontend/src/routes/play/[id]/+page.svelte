<script lang="ts">
	import { pageTitle } from '$lib/brand';
	import { onDestroy, onMount, untrack } from 'svelte';
	import { page } from '$app/stores';
	import { getContextClient } from '@urql/svelte';
	import { unwrap } from '$lib/api';
	import AiPromptCard from '$lib/components/AiPromptCard.svelte';
	import CallStack from '$lib/components/CallStack.svelte';
	import ConnectionBadge from '$lib/components/ConnectionBadge.svelte';
	import Grid from '$lib/components/Grid.svelte';
	import Palette from '$lib/components/Palette.svelte';
	import RunControls from '$lib/components/RunControls.svelte';
	import ResultModal from '$lib/components/ResultModal.svelte';
	import ShortcutsDialog from '$lib/components/ShortcutsDialog.svelte';
	import Stats from '$lib/components/Stats.svelte';
	import Tape from '$lib/components/Tape.svelte';
	import { describeError, lossReasonText, type DescribedError } from '$lib/errors';
		import { editKeyAction, globalKeyAction } from '$lib/globalKeys';
	import { canRedo, canUndo, emptyHistory, recordEdit, redo, undo, type EditHistory } from '$lib/history';
	import { nextMap } from '$lib/maps';
	import {
		CREATE_SESSION_MUTATION,
		MAPS_QUERY,
		PAUSE_MUTATION,
		RESET_MUTATION,
		RUN_MUTATION,
		SET_PROGRAM_MUTATION,
		STEP_MUTATION
	} from '$lib/queries';
	import { rememberSession, statusLine } from '$lib/sessions';
	import { serialRunner } from '$lib/serialRunner';
	import { clampSpeed } from '$lib/speed';
	import { cloneProgram, programsEqual, setSlot } from '$lib/program';
	import { buildSessionPrompt } from '$lib/prompt';
	import { createSessionStore } from '$lib/stores/session';
	import { createSessionSource } from '$lib/stores/source';
	import type { GameMap, Instruction, Program, Session } from '$lib/types';

	/** Quiet period after the last edit before the draft is saved to the server. */
	const AUTOSAVE_MS = 600;

	const sessionId = $page.params.id ?? '';
	const client = getContextClient();
	const store = createSessionStore(createSessionSource(client), sessionId);
	onDestroy(() => store.destroy());

	const origin = typeof window !== 'undefined' ? window.location.origin : '';
	const session = $derived($store.session);
	const connection = $derived($store.connection);

	let draft = $state<Program | null>(null);
	// Undo/redo stacks for draft edits (raw: programs are replaced, never mutated).
	let history = $state.raw<EditHistory>(emptyHistory);
	let selected = $state<Instruction | null>(null);
	let speedMs = $state(500);
	let busy = $state(false);
	let actionError = $state<DescribedError | null>(null);
	let dismissedAttempts = $state<number | null>(null);
	let showShortcuts = $state(false);
	let maps = $state<GameMap[]>([]);
	let initialised = false;
	let lastServer: Program | null = null;

	// Keep the editable draft in sync with the server program unless the user has unsaved edits.
	$effect(() => {
		const sp = session?.program;
		if (!sp) return;
		untrack(() => {
			if (draft === null) draft = cloneProgram(sp);
			else if (lastServer && programsEqual(draft, lastServer) && !programsEqual(draft, sp)) {
				// Someone else (an agent, another tab) changed the program: keep ours reachable via undo.
				history = recordEdit(history, draft);
				draft = cloneProgram(sp);
			}
			lastServer = cloneProgram(sp);
		});
	});

	$effect(() => {
		const s = session;
		if (!s || initialised) return;
		initialised = true;
		speedMs = clampSpeed(s.speedMs || 500);
		// Do not pop the result modal for an outcome that was already there on load.
		if (s.vm.status === 'WON' || s.vm.status === 'LOST') dismissedAttempts = s.attempts;
	});

	onMount(async () => {
		try {
			maps = (await unwrap<{ maps: GameMap[] }>(client.query(MAPS_QUERY, {}))).maps;
		} catch {
			// "Next map" is hidden without the map list
		}
	});
	const next = $derived(session ? nextMap(maps, session.mapId) : null);

	const callKeys = $derived(
		!draft?.subs.length ? '' : draft.subs.length === 1 ? '1 calls the subroutine,' : `1–${draft.subs.length} call subroutines,`
	);
	const dirty = $derived(!!draft && !!session && !programsEqual(draft, session.program));
	const status = $derived(session?.vm.status ?? 'READY');
	const terminal = $derived(status === 'WON' || status === 'LOST');
	const playing = $derived(session?.playing ?? false);
	// Reset only does something once the robot has moved or a run is under way.
	const canReset = $derived(playing || !!session && (terminal || session.vm.steps > 0));
	const showModal = $derived(terminal && !!session && dismissedAttempts !== session.attempts);
	const resultText = $derived(
		!session || !terminal
			? ''
			: status === 'WON'
				? `You won in ${session.vm.steps} steps.`
				: `Run lost. ${lossReasonText(session.vm.lossReason)}`
	);

	async function act(fn: () => Promise<Session | void>) {
		busy = true;
		actionError = null;
		try {
			const s = await fn();
			if (s) store.apply(s);
		} catch (e) {
			actionError = describeError(e);
		} finally {
			busy = false;
		}
	}

	/** Saves the draft when it differs from the server program and applies the result to the store. */
	async function saveIfDirty(): Promise<void> {
		if (!draft || !session || programsEqual(draft, session.program)) return;
		const data = await unwrap<{ setProgram: Session }>(
			client.mutation(SET_PROGRAM_MUTATION, { sessionID: sessionId, program: draft })
		);
		store.apply(data.setProgram);
	}

	// Autosave and Run/Step share one queue, so two saves never race and the newest draft wins.
	const flushSave = serialRunner(saveIfDirty);

	// Save a moment after the player stops editing, so the server (spectators, agents) stays in step
	// and the robot returns to the start as soon as the program changes.
	$effect(() => {
		if (!dirty || playing) return;
		const timer = setTimeout(() => {
			flushSave().catch((e) => (actionError = describeError(e)));
		}, AUTOSAVE_MS);
		return () => clearTimeout(timer);
	});

	const run = () =>
		act(async () => {
			await flushSave();
			return (await unwrap<{ run: Session }>(client.mutation(RUN_MUTATION, { sessionID: sessionId, speedMs }))).run;
		});
	const pause = () =>
		act(async () => (await unwrap<{ pause: Session }>(client.mutation(PAUSE_MUTATION, { sessionID: sessionId }))).pause);
	const stepOnce = () =>
		act(async () => {
			await flushSave();
			return (await unwrap<{ step: Session }>(client.mutation(STEP_MUTATION, { sessionID: sessionId }))).step;
		});
	const reset = () =>
		act(async () => {
			const s = (await unwrap<{ reset: Session }>(client.mutation(RESET_MUTATION, { sessionID: sessionId }))).reset;
			dismissedAttempts = s.attempts;
			return s;
		});

	/** Starts a session on the next map (standard order) with the same player name. */
	const playNext = () =>
		act(async () => {
			if (!next || !session) return;
			const data = await unwrap<{ createSession: Session }>(
				client.mutation(CREATE_SESSION_MUTATION, { mapID: next.id, displayName: session.displayName || null })
			);
			rememberSession(data.createSession.id, data.createSession.displayName, data.createSession.ownerToken);
			// Full navigation: this page keeps one session store for its lifetime.
			window.location.assign(`/play/${data.createSession.id}`);
		});

	const shortcutKeys = $derived<[string, string][]>([
		['Ctrl+Enter', 'Run, or pause while running'],
		['S', 'Step once (when no slot is focused)'],
		['R', 'Reset the robot (when no slot is focused)'],
		['Ctrl+Z', 'Undo a program edit'],
		['Ctrl+Shift+Z', 'Redo (Ctrl+Y also works)'],
		['?', 'Show this list'],
		['Esc', 'Close a dialog or menu'],
		['Tab', 'Focus a program slot'],
		['F', 'Slot: move forward'],
		['L / R', 'Slot: turn left / right'],
		...(draft?.subs.length ? [[draft.subs.length === 1 ? '1' : `1–${draft.subs.length}`, 'Slot: call a subroutine'] as [string, string]] : []),
		['Backspace', 'Slot: clear'],
		['Arrows', 'Slot: move between slots'],
		['Enter', 'Slot: open the instruction menu']
	]);

	function onWindowKey(e: KeyboardEvent) {
		if (showShortcuts || showModal) return;
		const editAction = editKeyAction(e);
		if (editAction) {
			e.preventDefault();
			if (editAction === 'undo') undoEdit();
			else if (editAction === 'redo') redoEdit();
			else toggleRun();
			return;
		}
		const action = globalKeyAction(e);
		if (!action) return;
		e.preventDefault();
		if (action === 'shortcuts') showShortcuts = true;
		else if (action === 'step') {
			if (!busy && !playing && !terminal) stepOnce();
		} else if (!busy) reset();
	}

	/** Applies a player edit to the draft, remembering the previous program for undo. */
	function edit(p: Program) {
		if (!draft || programsEqual(p, draft)) return;
		history = recordEdit(history, draft);
		draft = p;
	}

	function undoEdit() {
		if (!draft || playing) return;
		const r = undo(history, draft);
		if (!r) return;
		history = r.history;
		draft = r.program;
	}

	function redoEdit() {
		if (!draft || playing) return;
		const r = redo(history, draft);
		if (!r) return;
		history = r.history;
		draft = r.program;
	}

	/** Run, pause when running, or start over after a finished run (Ctrl/Cmd+Enter), like the primary button. */
	function toggleRun() {
		if (busy) return;
		if (playing) pause();
		else if (terminal) reset();
		else run();
	}

</script>

<svelte:head><title>{pageTitle(session?.map.name)}</title></svelte:head>
<svelte:window onkeydown={onWindowKey} />

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
				<h1 class="text-2xl font-semibold text-indigo-950">{session.map.name}</h1>
				<p class="text-sm text-slate-600">
					{session.displayName || 'Anonymous'} ·
					<a class="underline" href="/watch/{session.id}">spectator link</a>
				</p>
			</div>
			<ConnectionBadge state={connection} />
		</div>

		{#if actionError}
			<p role="alert" class="mt-3 rounded-lg border border-rose-300 bg-rose-50 px-3 py-2 text-sm text-rose-900" data-testid="action-error">
				<strong>{actionError.title}</strong>{#if actionError.code}<span class="ml-1 font-mono text-xs">[{actionError.code}]</span>{/if}
				{#if actionError.message}— {actionError.message}{/if}
			</p>
		{/if}

		<div class="mt-4 grid gap-6 lg:grid-cols-[minmax(0,1fr)_minmax(0,28rem)]">
			<section aria-label="Board" class="space-y-3 lg:sticky lg:top-4 lg:self-start">
				<Grid map={session.map} vm={session.vm} lastEvent={session.lastEvent} />
				<p class="text-sm text-slate-600">{session.map.description}</p>
				<p class="sr-only" role="status" aria-live="polite" data-testid="result-live">{resultText}</p>
			</section>

			<section aria-label="Program" class="space-y-4">
				<p
					class="rounded-lg px-3 py-2 text-sm font-semibold {status === 'WON'
						? 'bg-emerald-50 text-emerald-900'
						: status === 'LOST'
							? 'bg-rose-50 text-rose-900'
							: playing
								? 'bg-indigo-600 text-white'
								: 'bg-indigo-50 text-indigo-900'}"
					data-testid="status-line"
				>
					{statusLine({ playing, speedMs: session.speedMs, vm: session.vm })}
				</p>
				<Stats {session} />

				<div class="rounded-xl border border-indigo-200 bg-white p-3 space-y-3">
					<RunControls
						{playing}
						finished={terminal}
						{busy}
						{canReset}
						bind:speedMs
						onrun={run}
						onpause={pause}
						onstep={stepOnce}
						onreset={reset}
					/>
					{#if playing}
						<p class="text-xs text-slate-600">Editing is locked while the program runs. Pause to edit.</p>
					{/if}
				</div>

				{#if draft}
					<div class="rounded-xl border border-indigo-200 bg-white p-3 space-y-3">
						<Palette
							bind:selected
							subCount={draft.subs.length}
							disabled={playing}
							onerase={(s) => edit(setSlot(draft!, s.tape, s.slot, 'EMPTY'))}
						/>
						<Tape
							program={draft}
							{selected}
							disabled={playing}
							callStack={dirty ? [] : session.vm.callStack}
							lastEvent={dirty ? null : session.lastEvent}
							{status}
							onchange={edit}
							canUndo={canUndo(history)}
							canRedo={canRedo(history)}
							onundo={undoEdit}
							onredo={redoEdit}
						/>
					</div>
				{/if}

				<CallStack callStack={session.vm.callStack} map={session.map} lastEvent={session.lastEvent} />

				<div class="rounded-xl border border-indigo-200 bg-white p-4" data-testid="help">
					<h2 class="text-xs font-semibold uppercase tracking-widest text-indigo-900">Help</h2>
					<ul class="mt-2 list-disc space-y-1 pl-5 text-sm text-slate-700">
						<li>Click a slot to pick an instruction, or arm one in the palette and click slots to stamp it.</li>
						<li>Drag an instruction onto a slot to place it. Drag a slot to swap it with another, hold <kbd>Shift</kbd> to reorder a tape, <kbd>Alt</kbd> to copy, or drop it on the palette to clear it.</li>
						<li>With a slot focused: <kbd>F</kbd> forward, <kbd>L</kbd>/<kbd>R</kbd> turn, {callKeys} <kbd>Backspace</kbd> clear, arrows move.</li>
						<li><kbd>Ctrl</kbd>+<kbd>Z</kbd> undoes a program edit, <kbd>Ctrl</kbd>+<kbd>Enter</kbd> runs or pauses.</li>
						<li><kbd>S</kbd> steps once and <kbd>R</kbd> resets the robot when no slot is focused.</li>
						<li>Press <kbd>?</kbd> for shortcuts.</li>
					</ul>
					<a href="/learn" class="mt-2 inline-block text-sm font-medium text-indigo-700 underline">All rules →</a>
				</div>

				<AiPromptCard
					prompt={buildSessionPrompt(origin, session)}
					blurb="Copy this prompt into an AI chat and it will program the robot to win this session."
				/>
			</section>
		</div>

		{#if showModal}
			<ResultModal
				{status}
				reason={session.vm.lossReason}
				steps={session.vm.steps}
				maxSteps={session.map.maxSteps}
				bestSteps={session.bestSteps}
				mapId={session.mapId}
				sessionId={session.id}
				treats={{ collected: session.map.treats.length - session.vm.treatsRemaining.length, total: session.map.treats.length }}
				nextName={next?.name ?? null}
				{busy}
				onreset={reset}
				onnext={playNext}
				onclose={() => (dismissedAttempts = session.attempts)}
			/>
		{/if}
		{#if showShortcuts}
			<ShortcutsDialog keys={shortcutKeys} onclose={() => (showShortcuts = false)} />
		{/if}
	{/if}
</div>
