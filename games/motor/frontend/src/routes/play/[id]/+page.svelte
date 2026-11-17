<script lang="ts">
	import { pageTitle } from '$lib/brand';
	import { onDestroy, onMount, untrack } from 'svelte';
	import { page } from '$app/stores';
	import { getContextClient } from '@urql/svelte';
	import { unwrap } from '$lib/api';
	import AiPromptCard from '$lib/components/AiPromptCard.svelte';
	import ConnectionBadge from '$lib/components/ConnectionBadge.svelte';
	import Grid from '$lib/components/Grid.svelte';
	import Palette from '$lib/components/Palette.svelte';
	import RunControls from '$lib/components/RunControls.svelte';
	import ResultModal from '$lib/components/ResultModal.svelte';
	import ShortcutsDialog from '$lib/components/ShortcutsDialog.svelte';
	import Stats from '$lib/components/Stats.svelte';
	import Tape from '$lib/components/Tape.svelte';
	import TileLegend from '$lib/components/TileLegend.svelte';
	import { describeError, lossReasonText, type DescribedError } from '$lib/errors';
		import { editKeyAction, globalKeyAction } from '$lib/globalKeys';
	import { canRedo, canUndo, emptyHistory, recordEdit, redo, undo, type EditHistory } from '$lib/history';
	import { nextMap } from '$lib/maps';
	import { subCallKeys } from '$lib/instructions';
	import {
		CAN_PLAY_SESSION_QUERY,
		CREATE_SESSION_MUTATION,
		MAPS_QUERY,
		PAUSE_MUTATION,
		RESET_MUTATION,
		RUN_MUTATION,
		SET_PROGRAM_MUTATION,
		STEP_MUTATION
	} from '$lib/queries';
	import { newSessionName, ownerTokenFor, rememberSession, runTitle, statusLine } from '$lib/sessions';
	import { serialRunner } from '$lib/serialRunner';
	import { account } from '$lib/stores/account';
	import { replayTo, stepBackTarget } from '$lib/stepBack';
	import { initialSpeed, saveSpeed, savedSpeed } from '$lib/speed';
	import { blockedCallNotice } from '$lib/recursion';
	import { cloneProgram, programsEqual, setSlot } from '$lib/program';
	import { buildSessionPrompt } from '$lib/prompt';
	import { createSessionStore } from '$lib/stores/session';
	import { createSessionSource } from '$lib/stores/source';
	import type { GameMap, Instruction, Program, Session, StepEvent } from '$lib/types';

	/** Quiet period after the last edit before the draft is saved to the server. */
	const AUTOSAVE_MS = 600;

	const sessionId = $page.params.id ?? '';
	// The owner token this browser was given when it created the session. It is the
	// credential for every play call and for reading the program back.
	const ownerToken = ownerTokenFor(sessionId);
	const client = getContextClient();
	const store = createSessionStore(createSessionSource(client, ownerToken), sessionId);
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
	// A Back step replays the program from the start; editing is locked until it lands.
	let rewinding = $state(false);
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
		speedMs = initialSpeed(s, savedSpeed());
		// Do not pop the result modal for an outcome that was already there on load.
		if (s.vm.status === 'WON' || s.vm.status === 'LOST') dismissedAttempts = s.attempts;
	});

	onMount(async () => {
		try {
			maps = (await unwrap<{ maps: GameMap[] }>(client.query(MAPS_QUERY, {}))).maps;
		} catch {
			// "Next map" is hidden without the map list
		}
		// A session opened from someone else's link is readable but not playable.
		try {
			const res = await client.query<{ canPlaySession: boolean }>(CAN_PLAY_SESSION_QUERY, { id: sessionId, ownerToken }).toPromise();
			if (!res.error && res.data) canPlay = res.data.canPlaySession;
		} catch {
			// On a failed check keep the page usable; a refused save reports FORBIDDEN itself.
		}
	});
	const next = $derived(session ? nextMap(maps, session.mapId) : null);

	const callKeys = $derived(
		!draft?.subs.length ? '' : draft.subs.length === 1 ? '1 calls the subroutine,' : `1–${draft.subs.length} call subroutines,`
	);
	const dirty = $derived(!!draft && !!session?.program && !programsEqual(draft, session.program));
	const status = $derived(session?.vm.status ?? 'READY');
	// Running an empty Main tape only costs an attempt, so Run and Step wait for an instruction.
	const emptyMain = $derived(!!draft && !draft.main.some((i) => i !== 'EMPTY'));
	const terminal = $derived(status === 'WON' || status === 'LOST');
	const playing = $derived(session?.playing ?? false);
	// Whether this browser may play at all: a session created elsewhere and opened
	// from a shared link is readable but not playable.
	let canPlay = $state(true);
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

	/**
	 * Explains a step that a tile moved for the robot, so a carry, a mirror turn,
	 * a key pickup or a teleport never looks like a bug. Null for an ordinary step.
	 */
	function tileStepNotice(e: StepEvent | null): string | null {
		if (!e) return null;
		const parts: string[] = [];
		if (e.pushed) parts.push(`A conveyor carried the robot one extra cell to (${e.to.x},${e.to.y}).`);
		if (e.pushBlocked)
			parts.push('The conveyor could not carry the robot any further (rock, edge or closed door), so it stayed on the conveyor.');
		if (e.mirrored) parts.push(`A mirror turned the robot to face ${e.facingAfter.toLowerCase()}.`);
		if (e.collectedKey)
			parts.push(`Picked up key "${e.collectedKey}": every ${e.collectedKey.toUpperCase()} door is now open.`);
		if (e.teleported) parts.push(`A portal teleported the robot to (${e.teleported.x},${e.teleported.y}).`);
		return parts.length ? parts.join(' ') : null;
	}

	const tileNotice = $derived(tileStepNotice(session?.lastEvent ?? null));
	// The call-stack panel is gone, but a CALL that the map's recursion rule ignored
	// must still explain itself: the step was spent for nothing.
	const callNotice = $derived(dirty || !session ? null : blockedCallNotice(session.lastEvent));

	async function act(fn: () => Promise<Session | void>) {
		if (!canPlay) return;
		busy = true;
		actionError = null;
		try {
			const s = await fn();
			if (s) store.apply(s);
		} catch (e) {
			const d = describeError(e);
			// A session that turns out to be someone else's (created elsewhere, or
			// opened from a shared link) switches the page to read-only rather than
			// failing on every edit.
			if (d.code === 'FORBIDDEN') canPlay = false;
			actionError = d;
		} finally {
			busy = false;
		}
	}

	/** Saves the draft when it differs from the server program and applies the result to the store. */
	async function saveIfDirty(): Promise<void> {
		if (!draft || !session?.program || programsEqual(draft, session.program)) return;
		const data = await unwrap<{ setProgram: Session }>(
			client.mutation(SET_PROGRAM_MUTATION, { sessionID: sessionId, program: draft, ownerToken })
		);
		store.apply(data.setProgram);
	}

	// Autosave and Run/Step share one queue, so two saves never race and the newest draft wins.
	const flushSave = serialRunner(saveIfDirty);

	// Save a moment after the player stops editing, so the server (spectators, agents) stays in step
	// and the robot returns to the start as soon as the program changes.
	$effect(() => {
		if (!dirty || playing || rewinding || !canPlay) return;
		const timer = setTimeout(() => {
			flushSave().catch((e) => {
				const d = describeError(e);
				if (d.code === 'FORBIDDEN') canPlay = false;
				actionError = d;
			});
		}, AUTOSAVE_MS);
		return () => clearTimeout(timer);
	});

	const run = () =>
		act(async () => {
			await flushSave();
			return (await unwrap<{ run: Session }>(client.mutation(RUN_MUTATION, { sessionID: sessionId, speedMs, ownerToken }))).run;
		});
	const pause = () =>
		act(
			async () =>
				(await unwrap<{ pause: Session }>(client.mutation(PAUSE_MUTATION, { sessionID: sessionId, ownerToken }))).pause
		);
	const stepOnce = () =>
		act(async () => {
			await flushSave();
			return (await unwrap<{ step: Session }>(client.mutation(STEP_MUTATION, { sessionID: sessionId, ownerToken }))).step;
		});
	// Go back one step by replaying the program from the start for one step fewer (see lib/stepBack.ts).
	const canStepBack = $derived(!dirty && !!session && stepBackTarget(session.vm.steps) !== null);
	// Editing is locked while a step back replays, so an edit cannot reset the robot half way through it.
	const stepBack = () =>
		act(async () => {
			const target = session ? stepBackTarget(session.vm.steps) : null;
			if (target === null) return;
			await flushSave();
			rewinding = true;
			try {
				return await replayTo(
					target,
					async () => {
						const s = (await unwrap<{ reset: Session }>(client.mutation(RESET_MUTATION, { sessionID: sessionId, ownerToken }))).reset;
						dismissedAttempts = s.attempts;
						return s;
					},
					async () =>
						(await unwrap<{ step: Session }>(client.mutation(STEP_MUTATION, { sessionID: sessionId, ownerToken }))).step
				);
			} finally {
				rewinding = false;
			}
		});
	/** Remembers the chosen speed; while a run is playing it is restarted at the new speed (the server fixes the speed when a run starts). */
	function changeSpeed(ms: number) {
		saveSpeed(ms);
		if (!playing) return;
		void act(async () => {
			const paused = (await unwrap<{ pause: Session }>(client.mutation(PAUSE_MUTATION, { sessionID: sessionId, ownerToken }))).pause;
			if (paused.vm.status === 'WON' || paused.vm.status === 'LOST') return paused;
			return (await unwrap<{ run: Session }>(client.mutation(RUN_MUTATION, { sessionID: sessionId, speedMs: ms, ownerToken }))).run;
		});
	}
	const reset = () =>
		act(async () => {
			const s = (await unwrap<{ reset: Session }>(client.mutation(RESET_MUTATION, { sessionID: sessionId, ownerToken }))).reset;
			dismissedAttempts = s.attempts;
			return s;
		});

	/**
	 * Starts a session on the next map. The name is not inherited from this run: a guest keeps the
	 * name they typed (the saved one), and a signed-in player sends none so the server uses the
	 * account instead of an earlier guest alias.
	 */
	const playNext = () =>
		act(async () => {
			if (!next || !session) return;
			const data = await unwrap<{ createSession: Session }>(
				client.mutation(CREATE_SESSION_MUTATION, { mapID: next.id, displayName: newSessionName(!!$account.me) })
			);
			rememberSession(data.createSession.id, data.createSession.displayName, data.createSession.ownerToken);
			// Full navigation: this page keeps one session store for its lifetime.
			window.location.assign(`/play/${data.createSession.id}`);
		});

	const shortcutKeys = $derived<[string, string][]>([
		['Ctrl+Enter', 'Run, or pause while running'],
		['S', 'Step once (when no slot is focused)'],
		['B', 'Go back one step (when no slot is focused)'],
		['R', 'Reset the robot (when no slot is focused)'],
		['Ctrl+Z', 'Undo a program edit'],
		['Ctrl+Shift+Z', 'Redo (Ctrl+Y also works)'],
		['?', 'Show this list'],
		['Esc', 'Close a dialog or menu'],
		['Tab', 'Focus a program slot'],
		['F', 'Slot: move forward'],
		['L / R', 'Slot: turn left / right'],
		...(draft?.subs.length ? [[subCallKeys(draft.subs.length), 'Slot: call a subroutine'] as [string, string]] : []),
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
			if (!busy && !playing && !terminal && !emptyMain) stepOnce();
		} else if (action === 'back') {
			if (!busy && !playing && canStepBack) stepBack();
		} else if (!busy) reset();
	}

	/** Applies a player edit to the draft, remembering the previous program for undo. */
	function edit(p: Program) {
		if (!draft || programsEqual(p, draft)) return;
		history = recordEdit(history, draft);
		draft = p;
	}

	function undoEdit() {
		// busy covers a Back step's replay: an edit then would save the draft and reset the robot
		// half way through it, so the replay would land somewhere else than one step back.
		if (!draft || playing || busy) return;
		const r = undo(history, draft);
		if (!r) return;
		history = r.history;
		draft = r.program;
	}

	function redoEdit() {
		if (!draft || playing || busy) return;
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
		else if (!emptyMain) run();
	}

</script>

<svelte:head><title>{pageTitle(runTitle(session?.map.name, session && { playing, status }))}</title></svelte:head>
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

		{#if !canPlay}
			<p class="mt-3 rounded-lg border border-amber-300 bg-amber-50 px-3 py-2 text-sm text-amber-900" data-testid="read-only">
				<strong>Read-only.</strong> This run belongs to whoever created it, so only they can play it.
				You can still watch it live at <a class="underline" href="/watch/{session.id}">the spectator view</a>.
			</p>
		{/if}

		<div class="mt-4 grid gap-6 lg:grid-cols-[minmax(0,1fr)_minmax(0,28rem)]">
			<!-- On phones the board is pinned at the top so it stays visible while you edit and run: it fills the column width, and only a very tall map is capped (50vh) so the program panel keeps room. On wide screens it sits in its own sticky column. -->
			<section
				aria-label="Board"
				class="min-w-0 space-y-3 max-lg:sticky max-lg:top-0 max-lg:z-30 max-lg:-mx-4 max-lg:border-b max-lg:border-indigo-200 max-lg:px-4 max-lg:py-1 max-lg:bg-[var(--page-bg)] sm:max-lg:-mx-6 sm:max-lg:px-6 lg:sticky lg:top-4 lg:self-start"
			>
				<div class="mx-auto max-lg:max-w-[min(100%,calc(50vh*var(--ar)))]" style="--ar: {session.map.width / session.map.height}">
					<Grid map={session.map} vm={session.vm} lastEvent={session.lastEvent} />
				</div>
				<p class="text-sm text-slate-600 max-lg:hidden">{session.map.description}</p>
				<div class="max-lg:hidden"><TileLegend map={session.map} /></div>
				<p class="sr-only" role="status" aria-live="polite" data-testid="result-live">{resultText}</p>
			</section>

			<section aria-label="Program" class="min-w-0 space-y-3 sm:space-y-4">
				<!-- On phones the status pill and the stats share one wrapped line: the four stat cards cost ~90px of height there. -->
				<div class="flex flex-wrap items-center gap-x-3 gap-y-1 sm:block sm:space-y-4">
					<p
						class="rounded-lg px-3 py-2 text-sm font-semibold max-sm:rounded-md max-sm:px-2 max-sm:py-1 max-sm:text-xs {status === 'WON'
							? 'bg-emerald-50 text-emerald-900'
							: status === 'LOST'
								? 'bg-rose-50 text-rose-900'
								: playing
									? 'bg-indigo-600 text-white'
									: 'bg-indigo-50 text-indigo-900'}"
						data-testid="status-line"
					>
						{statusLine({ playing, speedMs: session.speedMs, vm: session.vm, emptyMain })}
					</p>
				{#if tileNotice}
					<p class="rounded-lg bg-indigo-50 px-3 py-1.5 text-xs text-indigo-900" data-testid="tile-notice">{tileNotice}</p>
				{/if}
					<Stats {session} />
				</div>

				{#if callNotice}
					<p class="rounded-lg bg-amber-50 px-2 py-1.5 text-xs text-amber-900" role="status" data-testid="blocked-call">
						{callNotice}
					</p>
				{/if}

				<div class="rounded-xl border border-indigo-200 bg-white p-3 space-y-3">
					<RunControls
						{playing}
						finished={terminal}
						busy={busy || !canPlay}
						{canReset}
						empty={emptyMain}
						{canStepBack}
						bind:speedMs
						onrun={run}
						onpause={pause}
						onstep={stepOnce}
						onstepback={stepBack}
						onspeed={changeSpeed}
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
							disabled={playing || !canPlay || rewinding}
							onerase={(s) => edit(setSlot(draft!, s.tape, s.slot, 'EMPTY'))}
						/>
						<Tape
							program={draft}
							{selected}
							disabled={playing || !canPlay || rewinding}
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

				<div class="lg:hidden"><TileLegend map={session.map} /></div>

				<div class="rounded-xl border border-indigo-200 bg-white p-4" data-testid="help">
					<h2 class="text-xs font-semibold uppercase tracking-widest text-indigo-900">Help</h2>
					<p class="mt-1 text-sm text-slate-700 lg:hidden">{session.map.description}</p>
					<ul class="mt-2 list-disc space-y-1 pl-5 text-sm text-slate-700">
						<li>Click a slot to pick an instruction, or arm one in the palette and click slots to stamp it.</li>
						<li>Drag an instruction onto a slot to place it. Drag a slot to swap it with another, hold <kbd>Shift</kbd> to reorder a tape, <kbd>Alt</kbd> to copy, or drop it on the palette to clear it.</li>
						<li class="max-sm:hidden">With a slot focused: <kbd>F</kbd> forward, <kbd>L</kbd>/<kbd>R</kbd> turn, {callKeys} <kbd>Backspace</kbd> clear, arrows move.</li>
						<li class="max-sm:hidden"><kbd>Ctrl</kbd>+<kbd>Z</kbd> undoes a program edit, <kbd>Ctrl</kbd>+<kbd>Enter</kbd> runs or pauses.</li>
						<li class="max-sm:hidden"><kbd>S</kbd> steps once, <kbd>B</kbd> goes back a step and <kbd>R</kbd> resets the robot when no slot is focused.</li>
						<li>
							Some maps have tiles that move the robot for you: a <strong>conveyor</strong> carries it one extra cell
							(<code>pushed</code>), a <strong>mirror</strong> turns it (<code>mirrored</code>), a <strong>portal</strong> hops it to its
							twin (<code>teleported</code>), and a <strong>key</strong> opens every matching door (<code>collectedKey</code>). A blocked
							carry (<code>pushBlocked</code>) just means the robot stayed on the conveyor. Each is still one step.
						</li>
						<li class="max-sm:hidden">Press <kbd>?</kbd> for shortcuts.</li>
					</ul>
					<a href="/learn" class="mt-2 inline-block text-sm font-medium text-indigo-700 underline">All rules →</a>
				</div>

				<AiPromptCard
					prompt={buildSessionPrompt(origin, { ...session, ownerToken })}
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
