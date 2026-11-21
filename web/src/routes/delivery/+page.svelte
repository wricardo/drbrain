<script lang="ts">
	import { onDestroy, onMount } from 'svelte';
	import { api, ApiError, SNAPSHOT_FIELDS, type Direction, type SessionSummary, type Snapshot } from '$lib/delivery/api';
	import { pageTitle } from '$lib/delivery/brand';
	import { subscribeToSession } from '$lib/delivery/subscribe';
	import { forgetSession, lastSessionId, rememberSession } from '$lib/delivery/ownerTokens';
	import Map from '$lib/delivery/Map.svelte';
	import Panel from '$lib/delivery/Panel.svelte';
	import FleetPanel from '$lib/delivery/FleetPanel.svelte';
	import OrdersPanel from '$lib/delivery/OrdersPanel.svelte';
	import Hero from '@drbrain/ui/components/Hero.svelte';
	import Button from '@drbrain/ui/components/Button.svelte';
	import EmptyState from '@drbrain/ui/components/EmptyState.svelte';
	import Skeleton from '@drbrain/ui/components/Skeleton.svelte';
	import ProgressPanel from '$lib/delivery/ProgressPanel.svelte';

	// --- state ---------------------------------------------------------------

	let snap = $state<Snapshot | null>(null);
	let error = $state<string | null>(null);
	let selectedCarId = $state<string | null>(null);
	/** Which order's route is highlighted on the map. */
	let selectedOrderId = $state<string | null>(null);
	let busy = $state(false);

	/** False when the open session belongs to someone else: the page then only watches. */
	let canPlay = $state(true);

	/** Saves on disk, shown on the start screen. */
	let saves = $state<SessionSummary[]>([]);
	let savesLoading = $state(true);
	let savesFailed = $state(false);
	let opening = $state(false);

	async function loadSaves() {
		savesLoading = true;
		savesFailed = false;
		try {
			// The client already unwraps the GraphQL envelope and guarantees an
			// array, so assigning it directly is safe here.
			saves = await api.sessions();
		} catch {
			savesFailed = true;
			saves = [];
		} finally {
			savesLoading = false;
		}
	}

	async function openSave(id: string) {
		if (opening) return;
		opening = true;
		try {
			const s = await api.session(id);
			canPlay = await api.canPlay(s.id);
			if (canPlay) rememberSession(s.id);
			snap = s;
			selectedCarId = s.cars[0]?.id ?? null;
			error = null;
		} catch (e) {
			error = e instanceof ApiError ? `${e.message} (${e.code})` : String(e);
			void loadSaves();
		} finally {
			opening = false;
		}
	}

	/** Speed presets the server accepts. */
	const SPEEDS = [
		{ value: 0, label: 'pause' },
		{ value: 1, label: '1×' },
		{ value: 2, label: '2×' },
		{ value: 4, label: '4×' }
	];

	// The zero-speed button doubles as pause/resume, so its label is derived
	// from state rather than fixed: a button that says "pause" while paused is
	// ambiguous about what clicking does.

	const selectedCar = $derived(
		snap?.cars.find((c) => c.id === selectedCarId) ?? snap?.cars[0] ?? null
	);

	/** The order whose route is highlighted, falling back to the first open one. */
	const highlightedOrder = $derived.by(() => {
		if (!snap) return null;
		const open = snap.orders.filter((o) => o.state !== 'DONE' && o.state !== 'EXPIRED');
		if (selectedOrderId) {
			const picked = open.find((o) => o.id === selectedOrderId);
			if (picked) return picked;
		}
		return open[0] ?? null;
	});

	/** Recent events, newest first, capped so the feed cannot grow unbounded. */
	const feed = $derived(
		snap ? [...snap.events].slice(-40).reverse() : []
	);

	/**
	 * Purchasable classes come from the server, never from a hardcoded list:
	 * prices live in the city file, so a stale client would quote a cost the
	 * server does not charge. Starter classes (price 0) are hidden because the
	 * player already has one.
	 */
	const buyable = $derived(
		(snap?.carClasses ?? [])
			.filter((c) => c.price > 0)
			.map((c) => ({
				cls: c.className,
				price: c.price,
				affordable: c.affordable && c.unlocked,
				unlocked: c.unlocked,
				capacity: c.capacity,
				maxBattery: c.maxBattery
			}))
	);

	// --- networking ----------------------------------------------------------

	/**
	 * How long a car takes to glide one tile when the clock is paused, i.e. when
	 * the player is driving by hand.
	 *
	 * This must not be zero. Sessions start paused (the game is manual driving),
	 * so a paused step is the *normal* way a car moves — and `moveMs` used to be
	 * derived from the poll interval, which is 0 while paused because "nothing
	 * moves". The result was that every keystroke teleported the car a whole
	 * tile and only a running clock ever animated. A manual step is a single
	 * discrete event, so it needs its own fixed duration rather than a cadence:
	 * long enough to read as driving, short enough that the next keystroke does
	 * not queue behind it.
	 */
	const STEP_GLIDE_MS = 160;

	/*
	 * History note: this page used to poll. It kept a `pollMs` cadence of one
	 * tick (tickMs / speed) because a fixed 400 ms sampled an interval that does
	 * not divide the tick — at 1x it skipped alternately one and two ticks, and
	 * at 4x it skipped five or six, which is why cars used to jump. The live
	 * subscription below makes that moot: the server pushes exactly one frame
	 * per tick, so the cadence question cannot arise. The glide timing still
	 * derives from the tick length, which is why tickMs is still read here.
	 */

	/**
	 * The id of the session being watched, or null on the picker screen.
	 *
	 * A derived value rather than reading `snap?.id` inside the effect below.
	 * That distinction is load-bearing: an effect that reads `snap` re-runs on
	 * every assignment to it, and since every pushed frame reassigns `snap`,
	 * such an effect would tear down and reopen the stream on every tick — a
	 * reconnect storm (measured: 1204 requests in 6 seconds). A derived value
	 * compares by value, so the effect runs only when the id really changes.
	 */
	const watchedId = $derived(snap?.id ?? null);

	// --- live updates --------------------------------------------------------

	/**
	 * Stop function for the current subscription, if any.
	 */
	let unsubscribe: (() => void) | null = null;
	/** Reconnect timer, used only after a stream drops unexpectedly. */
	let reconnectTimer: ReturnType<typeof setTimeout> | undefined;
	/** Consecutive failures, for exponential backoff on reconnect. */
	let streamFailures = 0;
	/**
	 * True when the server refused the stream or streams keep failing.
	 */
	let streamUnavailable = $state(false);

	/**
	 * Highest tick this tab has rendered.
	 *
	 * A frame that would move the clock backwards is dropped. Frames are ordered
	 * on one connection, but a mutation's own response can land between two
	 * frames, so a late frame describing an older tick must not rewind the map.
	 * Same-tick frames are kept: a mutation like `assignOrder` changes state
	 * without advancing the clock, and that state change is exactly what the
	 * next frame carries.
	 */
	let renderedTick = 0;

	/**
	 * How long a car takes to slide from the tile it was on to the tile the
	 * latest snapshot puts it on.
	 *
	 * A running clock pushes once per tick, so the glide lasts exactly one tick
	 * and the next frame lands just as the car arrives: continuous motion with
	 * no interpolation needed.
	 *
	 * A paused clock is the default state and the normal way the game is
	 * played, and it still has to animate. Deriving this from the poll interval
	 * gave 0 while paused (a paused city "has nothing to animate"), so every
	 * manual step teleported the car a whole tile — the bug this used to cause.
	 * A step is a discrete event with no cadence to match, so it gets a fixed
	 * duration instead: a running clock keeps the tick-synced glide, a paused
	 * one glides for STEP_GLIDE_MS.
	 */
	const moveMs = $derived.by(() => {
		if (!snap) return 0;
		if (!snap.paused && snap.speed > 0) return Math.max(50, Math.floor(snap.tickMs / snap.speed));
		return STEP_GLIDE_MS;
	});

	/**
	 * Opens the live stream for the current session.
	 *
	 * This replaces polling entirely. The server pushes a frame on every tick
	 * and on every mutation, so a paused city updates the instant a key is
	 * pressed and a running one updates once per tick with no request per
	 * frame. Only the transport is different from the old poll loop: the
	 * snapshot shape is identical, so every consumer downstream is unchanged.
	 */
	function startStream(id: string) {
		stopStream();

		unsubscribe = subscribeToSession(id, SNAPSHOT_FIELDS, {
			next(next) {
				// Ignore frames for a session the player already left, and frames
				// that would rewind the clock (see renderedTick).
				if (snap?.id !== id) return;
				if (next.tick < renderedTick) return;
				streamFailures = 0;
				streamUnavailable = false;
				renderedTick = next.tick;
				snap = next;
			},
			closed(reason) {
				unsubscribe = null;
				if (snap?.id !== id) return;
				// Back off rather than hot-looping: the usual cause is the server
				// restarting or the session being evicted.
				const delay = Math.min(8000, 250 * 2 ** streamFailures);
				streamFailures++;
				if (streamFailures >= 3) streamUnavailable = true;
				runtimeLog(reason);
				reconnectTimer = setTimeout(() => {
					reconnectTimer = undefined;
					// Reconnect only if the player is still on this session.
					// Guarded by watchedId rather than snap so a frame arriving
					// meanwhile cannot spawn a second stream.
					if (watchedId === id) startStream(id);
				}, delay);
			}
		});
	}

	function stopStream() {
		unsubscribe?.();
		unsubscribe = null;
		if (reconnectTimer) clearTimeout(reconnectTimer);
		reconnectTimer = undefined;
	}

	/**
	 * runtimeLog records a stream problem without spamming the console.
	 *
	 * A dropped stream is recoverable and the UI shows its own indicator, so
	 * this is a debug-level note rather than an error the player must act on.
	 */
	function runtimeLog(reason: string) {
		if (typeof console !== 'undefined') console.debug('[drbrain] live stream:', reason);
	}

	/**
	 * Every mutation returns the full snapshot, so the UI never needs a
	 * follow-up read. That keeps the client a pure renderer of server truth.
	 *
	 * `renderedTick` is advanced here too, so a frame built before this mutation
	 * landed cannot rewind the clock when it arrives just after.
	 */
	async function apply(fn: () => Promise<Snapshot>) {
		busy = true;
		try {
			snap = await fn();
			renderedTick = snap.tick;
			error = null;
		} catch (e) {
			error = e instanceof ApiError ? `${e.message} (${e.code})` : String(e);
		} finally {
			busy = false;
		}
	}

	async function startSession() {
		if (opening) return;
		opening = true;
		try {
			const s = await api.createSession(); // api remembers the session and its owner token
			canPlay = true;
			snap = s;
			selectedCarId = s.cars[0]?.id ?? null;
			error = null;
		} catch (e) {
			error = e instanceof ApiError ? `${e.message} (${e.code})` : String(e);
		} finally {
			opening = false;
		}
	}

	async function resume() {
		const id = lastSessionId();
		if (!id) return;
		try {
			snap = await api.session(id);
			canPlay = await api.canPlay(id);
			selectedCarId = snap.cars[0]?.id ?? null;
		} catch {
			// Stale id (deleted save): fall through to the start screen.
			forgetSession();
		}
	}

	/** Leave the current city and return to the picker. The save stays on disk. */
	async function leave() {
		if (snap && canPlay) {
			// Persist before walking away so nothing since the last tick is lost.
			try {
				await api.saveSession(snap.id);
			} catch {
				/* best effort: the server also saves on shutdown */
			}
		}
		forgetSession();
		snap = null;
		selectedCarId = null;
		selectedOrderId = null;
		void loadSaves();
	}

	/**
	 * Drive one step: queue the direction and advance the clock.
	 *
	 * The API separates commands from the clock, but a player pressing `s`
	 * expects the car to move. Doing both keeps the UI feeling like driving
	 * while the engine keeps its deterministic split. `step()` on its own is
	 * still available for setting up multi-car moves.
	 */
	async function drive(dir: Direction) {
		if (!snap || !canPlay || !selectedCar) return;
		const id = snap.id;
		const carId = selectedCar.id;
		busy = true;
		try {
			await api.moveCar(id, carId, dir);
			// Two round trips, so a frame may describe the state in between;
			// only the tick's own response is authoritative for this step.
			snap = await api.tick(id, 1);
			renderedTick = snap.tick;
			error = null;
		} catch (e) {
			error = e instanceof ApiError ? `${e.message} (${e.code})` : String(e);
		} finally {
			busy = false;
		}
	}

	/**
	 * Queue a step for the selected car WITHOUT advancing the clock.
	 *
	 * This is how several cars are set up to move on the same tick: queue a
	 * direction for each, then press space once. The engine keeps commands and
	 * the clock separate for exactly this, so the UI must not hide it.
	 */
	function queueStep(dir: Direction) {
		if (!snap || !canPlay || !selectedCar) return;
		void apply(() => api.moveCar(snap!.id, selectedCar!.id, dir));
	}

	function step(n = 1) {
		if (!snap || !canPlay) return;
		void apply(() => api.tick(snap!.id, n));
	}

	function setSpeed(speed: number) {
		if (!snap || !canPlay) return;
		void apply(() => api.setSpeed(snap!.id, speed));
	}

	function assign(orderId: string) {
		if (!snap || !canPlay || !selectedCar) return;
		void apply(() => api.assignOrder(snap!.id, orderId, selectedCar!.id));
	}

	function unassign(orderId: string) {
		if (!snap || !canPlay) return;
		void apply(() => api.unassignOrder(snap!.id, orderId));
	}

	function tow(carId: string) {
		if (!snap || !canPlay) return;
		void apply(() => api.towCar(snap!.id, carId));
	}

	function buy(cls: string) {
		if (!snap || !canPlay) return;
		void apply(() => api.buyCar(snap!.id, cls));
	}

	function build(buildId: string) {
		if (!snap || !canPlay) return;
		void apply(() => api.build(snap!.id, buildId));
	}

	function cycleCar(delta: number) {
		if (!snap || snap.cars.length === 0) return;
		const i = snap.cars.findIndex((c) => c.id === selectedCarId);
		const next = (i + delta + snap.cars.length) % snap.cars.length;
		selectedCarId = snap.cars[next].id;
	}

	// --- keyboard ------------------------------------------------------------

	const KEY_DIRS: Record<string, Direction> = {
		w: 'NORTH',
		ArrowUp: 'NORTH',
		s: 'SOUTH',
		ArrowDown: 'SOUTH',
		a: 'WEST',
		ArrowLeft: 'WEST',
		d: 'EAST',
		ArrowRight: 'EAST'
	};

	function onKey(e: KeyboardEvent) {
		if (!snap || e.defaultPrevented || e.altKey || e.ctrlKey || e.metaKey) return;
		const target = e.target as HTMLElement | null;
		if (target?.closest('input, textarea, select, button, a, [contenteditable], [role="region"]')) return;

		const dir = KEY_DIRS[e.key];
		if (dir) {
			e.preventDefault();
			// A direction key drives: queue the move and step once. Shift queues
			// the step without advancing, which is how a multi-car player sets up
			// several cars for the same tick.
			if (e.shiftKey) {
				queueStep(dir);
			} else {
				void drive(dir);
			}
			return;
		}
		if (e.key === ' ' || e.key === 'Enter') {
			e.preventDefault();
			step(1);
			return;
		}
		if (e.key.toLowerCase() === 'c') {
			e.preventDefault();
			cycleCar(e.shiftKey ? -1 : 1);
			return;
		}
		if (e.key === 'p' || e.key === 'P') {
			setSpeed(snap.paused ? 1 : 0);
		}
	}

	// --- lifecycle -----------------------------------------------------------

	/**
	 * Keeps exactly one live stream open for the current session.
	 *
	 * Keyed on the session id rather than called from each of the seven places
	 * that assign `snap` (resume, open, create, leave, mutations): missing one
	 * would leave the page silently frozen, which is exactly the class of bug
	 * that made polling fragile. An effect on the id cannot be missed.
	 *
	 * It also closes the stream when the player leaves the city, so a background
	 * tab is not holding a connection to a session nobody is watching.
	 */
	$effect(() => {
		// Read ONLY watchedId here. Touching `snap` would restart the stream on
		// every frame, because each frame reassigns it.
		const id = watchedId;
		if (!id) {
			stopStream();
			return;
		}
		startStream(id);
		return () => stopStream();
	});

	onMount(() => {
		void resume();
		void loadSaves();
		window.addEventListener('keydown', onKey);
	});

	onDestroy(() => {
		stopStream();
		window.removeEventListener('keydown', onKey);
	});
</script>

<svelte:head>
	<title>{snap ? pageTitle() : "Delivery · drbrain"}</title>
</svelte:head>

{#if !snap}
	<div class="db-container game-landing">
		<Hero
			label="Delivery"
			title="Run the fleet."
			accent="Grow the city."
			description="Drive a delivery van, pick up orders and earn enough to expand your fleet. The city grows as you deliver."
			image="/art/delivery.webp"
			wideArt
		>
			<Button disabled={opening} onclick={startSession} data-testid="start-city">
				{opening ? 'Opening city…' : 'Start a new city'} <span aria-hidden="true">→</span>
			</Button>
		</Hero>
		{#if error}<p class="mt-4 rounded-lg border border-rose-200 bg-rose-50 px-4 py-3 text-sm text-rose-900" role="alert">{error}</p>{/if}
		<section class="py-8" aria-labelledby="saved-cities">
			<h2 id="saved-cities" class="mb-3 text-2xl font-bold tracking-tight">Saved cities</h2>
			{#if savesLoading}
				<Skeleton label="Loading cities…" />
			{:else if savesFailed}
				<EmptyState title="Could not load cities" description="Try loading your saved cities again.">
					<Button variant="secondary" onclick={loadSaves}>Retry</Button>
				</EmptyState>
			{:else if saves.length === 0}
				<EmptyState title="No saved cities yet" description="Start your first city to build a fleet." />
			{:else}
				<ul class="db-panel divide-y divide-edge">
					{#each saves as s (s.id)}
						<li>
							<button
								type="button"
								class="group flex min-h-14 w-full items-center justify-between gap-3 px-4 py-3 text-left transition-colors hover:bg-surface-muted disabled:opacity-60"
								disabled={opening}
								onclick={() => openSave(s.id)}
								title={`Open ${s.id}`}
							>
								<span class="min-w-0">
									<span class="block truncate text-sm text-body">
										{s.ownerDisplayName !== 'Guest' ? `${s.ownerDisplayName}’s city` : s.id}
										{#if s.mine}
											<span class="ml-1 rounded bg-[var(--color-charger)]/15 px-1.5 py-0.5 text-[11px] font-semibold text-[var(--color-charger)]">yours</span>
										{/if}
									</span>
									{#if s.ownerDisplayName !== 'Guest'}
										<span class="block truncate text-[11px] text-faint">{s.id}</span>
									{/if}
									<span class="text-xs text-faint">
										tick {s.tick} · {s.deliveries} delivered · rep {s.reputation.toFixed(2)}
									</span>
								</span>
								<span class="flex shrink-0 items-center gap-2">
									<span class="text-sm text-[var(--color-money)]">${s.money}</span>
									<span
										class="rounded border border-edge-strong px-2 py-0.5 text-xs text-muted transition-colors group-hover:border-[var(--color-charger)] group-hover:text-[var(--color-charger)]"
									>
										Open
									</span>
								</span>
							</button>
							<!-- Outside the button: a link inside a button is invalid HTML. -->
							{#if s.ownerHandle && !s.mine}
								<a href={`/u/${s.ownerHandle}`} class="-mt-1.5 block px-4 pb-2 text-xs text-muted hover:text-body">@{s.ownerHandle}</a>
							{/if}
						</li>
					{/each}
				</ul>
			{/if}

		</section>
	</div>
{:else}
	<div class="db-container flex min-h-0 flex-col gap-3 py-4">
		<header class="db-panel game-run-title">
			<h1 class="text-xl">Fleet dashboard</h1>
			<div class="flex flex-wrap items-center gap-x-4 gap-y-1 text-sm tabular-nums">
				<span class="text-faint">Tick <strong class="text-body">{snap.tick}</strong></span>
				<span class="text-[var(--color-money)]">${snap.ledger.money}</span>
				{#if snap.ledger.upkeepDue > 0}
					<!--
						The running cost has to be visible or it is just a mysterious drop
						in the money number. Shown as "due", not "paid", so the player can
						act on it (park the car) before the charge lands.
					-->
					<span
						class="text-faint"
						data-upkeep-due={snap.ledger.upkeepDue}
						title="Fleet upkeep charged next interval. Park cars on a depot tile to stop it."
					>
						−<span class="text-[var(--color-warn)]">${snap.ledger.upkeepDue}</span>/ivl
					</span>
				{/if}
				<span class="text-faint">
					Reputation <span class="text-body">{snap.ledger.reputation.toFixed(2)}</span>
				</span>
				<span class="text-faint">
					Deliveries <span class="text-body">{snap.ledger.deliveries}</span>
				</span>
				{#if snap.ledger.onTimeStreak > 0}
					<span class="text-[var(--color-money)]">streak {snap.ledger.onTimeStreak}</span>
				{/if}
				<!--
					The live stream can drop (a server restart, or the session being
					evicted). Without a marker the page would simply stop updating,
					which looks like a frozen game rather than a transport problem —
					exactly the silent failure the old poll loop had.
				-->
				{#if streamUnavailable}
					<span
						class="text-[var(--color-warn)]"
						data-stream="reconnecting"
						title="Live updates stopped. Retrying; the state shown may be stale."
					>
						reconnecting…
					</span>
				{/if}
			</div>

			<div class="flex flex-wrap items-center gap-1.5">
				<button
					type="button"
					class="min-h-11 rounded-lg px-2.5 text-sm font-semibold text-faint hover:bg-surface-muted hover:text-body"
					onclick={leave}
					title="Save and return to the city picker"
				>
					Cities
				</button>
				{#if canPlay}
					<span class="h-4 w-px bg-track"></span>
					{#each SPEEDS as s (s.value)}
						<button
							type="button"
							class="min-h-11 min-w-9 rounded-lg px-2 text-sm font-semibold transition-colors {snap.speed === s.value
								? 'bg-[var(--color-charger)]/20 text-[var(--color-charger)]'
								: 'text-faint hover:text-body'}"
							onclick={() => setSpeed(s.value)}
							aria-pressed={snap.speed === s.value}
							title={s.value === 0 ? 'Pause the clock' : `Run at ${s.value}× speed`}
						>
							{s.value === 0 ? (snap.paused ? 'paused' : 'pause') : s.label}
						</button>
					{/each}
					<span class="mx-1 h-4 w-px bg-track"></span>
					<button
						type="button"
						class="min-h-11 rounded-lg border border-edge-strong px-2.5 text-sm font-semibold text-muted hover:bg-surface-muted hover:text-body"
						onclick={() => step(1)}>step</button
					>
					<button
						type="button"
						class="min-h-11 rounded-lg border border-edge-strong px-2.5 text-sm font-semibold text-muted hover:bg-surface-muted hover:text-body"
						onclick={() => step(10)}>+10</button
					>
				{/if}
			</div>
		</header>

		{#if !canPlay}
			<p class="rounded-lg border border-edge bg-surface-muted px-4 py-2 text-sm text-muted" role="status" data-testid="spectator-notice">
				Viewing someone else's save — read-only
			</p>
		{/if}

		{#if error}
			<div
				class="rounded-lg border border-[var(--color-danger)]/40 bg-[var(--color-danger)]/10 px-4 py-2 text-sm text-[var(--color-danger)]" role="alert"
			>
				{error}
			</div>
		{/if}

		<!-- body -->
		<div
			class="grid min-h-0 grid-cols-[minmax(0,1fr)] gap-3 pb-5 lg:grid-cols-[minmax(0,1fr)_360px]"
		>
			<div class="flex min-w-0 flex-col gap-3">
				<!-- svelte-ignore a11y_no_noninteractive_tabindex (Keyboard users can scroll a wide map.) -->
				<div class="w-fit max-w-full self-center overflow-x-auto rounded-xl" tabindex="0" role="region" aria-label="Scrollable city map">
					<Map
						city={snap.city}
						cars={snap.cars}
						selectedCarId={selectedCar?.id ?? null}
						highlightKitchen={highlightedOrder?.kitchenId ?? null}
						highlightCustomer={highlightedOrder?.customerId ?? null}
						closures={snap.closures}
						{moveMs}
						onSelectCar={(id) => (selectedCarId = id)}
					/>
				</div>

				{#if canPlay}
					<Panel title="Controls" summary={selectedCar ? `driving ${selectedCar.name}` : ''}>
						<div class="flex flex-wrap items-center gap-x-4 gap-y-2 px-3 py-2 text-xs text-muted">
							<!-- A D-pad, because keyboard-only excludes mouse and touch. -->
							<div class="grid grid-cols-3 gap-1" role="group" aria-label="Drive">
								<span></span>
								<button
									type="button"
									class="size-11 rounded-lg border border-edge-strong text-base font-semibold text-body hover:border-[var(--color-charger)] hover:text-[var(--color-charger)]"
									onclick={() => drive('NORTH')}
									aria-label="Move north"
									title="Move north (w)">↑</button
								>
								<span></span>
								<button
									type="button"
									class="size-11 rounded-lg border border-edge-strong text-base font-semibold text-body hover:border-[var(--color-charger)] hover:text-[var(--color-charger)]"
									onclick={() => drive('WEST')}
									aria-label="Move west"
									title="Move west (a)">←</button
								>
								<button
									type="button"
									class="size-11 rounded-lg border border-edge-strong text-base font-semibold text-body hover:border-[var(--color-charger)] hover:text-[var(--color-charger)]"
									onclick={() => step(1)}
									aria-label="Advance one tick"
									title="Advance one tick (space)">•</button
								>
								<button
									type="button"
									class="size-11 rounded-lg border border-edge-strong text-base font-semibold text-body hover:border-[var(--color-charger)] hover:text-[var(--color-charger)]"
									onclick={() => drive('EAST')}
									aria-label="Move east"
									title="Move east (d)">→</button
								>
								<span></span>
								<button
									type="button"
									class="size-11 rounded-lg border border-edge-strong text-base font-semibold text-body hover:border-[var(--color-charger)] hover:text-[var(--color-charger)]"
									onclick={() => drive('SOUTH')}
									aria-label="Move south"
									title="Move south (s)">↓</button
								>
								<span></span>
							</div>

							<div class="flex flex-col gap-0.5">
								<span><kbd class="text-body">w a s d</kbd> drive</span>
								<span><kbd class="text-body">shift</kbd> + direction: queue only</span>
								<span><kbd class="text-body">space</kbd> tick only</span>
								<span><kbd class="text-body">c</kbd> switch car</span>
								<span><kbd class="text-body">p</kbd> pause/resume</span>
							</div>
						</div>
						{#if selectedCar}
							<div class="border-t border-edge px-3 py-1.5 text-xs text-muted">
								driving <span class="text-body">{selectedCar.name}</span>
								{#if selectedCar.batteryPct <= 25}
									<span class="text-[var(--color-warn)]"> — low battery</span>
								{/if}
							</div>
						{/if}
					</Panel>
				{/if}

				<Panel title="Activity" summary="{feed.length} event{feed.length === 1 ? '' : 's'}">
					<ul class="max-h-44 overflow-y-auto px-3 py-1">
						{#each feed as e, i (i)}
							<li class="py-0.5 text-xs text-muted">
								<span class="text-faint">t{e.tick}</span>
								{e.message}
							</li>
						{/each}
						{#if feed.length === 0}
							<li class="py-0.5 text-xs text-faint">Nothing has happened yet.</li>
						{/if}
					</ul>
				</Panel>
			</div>

			<div class="flex min-h-0 flex-col gap-3 overflow-y-auto">
				<FleetPanel
					cars={snap.cars}
					selectedCarId={selectedCar?.id ?? null}
					onSelect={(id) => (selectedCarId = id)}
					onTow={canPlay ? tow : undefined}
					buyable={canPlay ? buyable : []}
					onBuy={canPlay ? buy : undefined}
				/>
				<OrdersPanel
					orders={snap.orders}
					selectedCarId={selectedCar?.id ?? null}
					selectedOrderId={highlightedOrder?.id ?? null}
					onAssign={canPlay ? assign : undefined}
					onUnassign={canPlay ? unassign : undefined}
					onSelectOrder={(id) => (selectedOrderId = id)}
				/>
				<ProgressPanel
					achievements={snap.achievements}
					unlocks={snap.unlocks}
					builds={snap.builds}
					onBuild={canPlay ? build : undefined}
				/>
			</div>
		</div>
	</div>
{/if}
