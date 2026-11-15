<script lang="ts">
	import { onDestroy, onMount } from 'svelte';
	import { api, ApiError, type Direction, type SessionSummary, type Snapshot } from '$lib/api';
	import Map from '$lib/Map.svelte';
	import FleetPanel from '$lib/FleetPanel.svelte';
	import OrdersPanel from '$lib/OrdersPanel.svelte';
	import ProgressPanel from '$lib/ProgressPanel.svelte';

	// --- state ---------------------------------------------------------------

	let snap = $state<Snapshot | null>(null);
	let error = $state<string | null>(null);
	let selectedCarId = $state<string | null>(null);
	/** Which order's route is highlighted on the map. */
	let selectedOrderId = $state<string | null>(null);
	let busy = $state(false);
	let feedOpen = $state(true);

	const SESSION_KEY = 'drbrain:session';

	/** Saves on disk, shown on the start screen. */
	let saves = $state<SessionSummary[]>([]);
	let savesLoading = $state(false);

	async function loadSaves() {
		savesLoading = true;
		try {
			// The client already unwraps the GraphQL envelope and guarantees an
			// array, so assigning it directly is safe here.
			saves = await api.sessions();
		} catch {
			// A listing failure must not block starting a new city.
			saves = [];
		} finally {
			savesLoading = false;
		}
	}

	async function openSave(id: string) {
		try {
			const s = await api.session(id);
			localStorage.setItem(SESSION_KEY, s.id);
			snap = s;
			selectedCarId = s.cars[0]?.id ?? null;
			error = null;
		} catch (e) {
			error = e instanceof ApiError ? `${e.message} (${e.code})` : String(e);
			void loadSaves();
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

	/** Poll cadence: the clock runs on the server, so this is the display lag. */
	const RUNNING_POLL_MS = 400;
	const PAUSED_POLL_MS = 1200;

	/**
	 * Every mutation returns the full snapshot, so the UI never needs a
	 * follow-up read. That keeps the client a pure renderer of server truth.
	 *
	 * `version` is bumped so an in-flight poll cannot overwrite this result
	 * with state it read before the mutation landed.
	 */
	async function apply(fn: () => Promise<Snapshot>) {
		busy = true;
		try {
			snap = await fn();
			version++;
			error = null;
		} catch (e) {
			error = e instanceof ApiError ? `${e.message} (${e.code})` : String(e);
		} finally {
			busy = false;
		}
	}

	async function startSession() {
		try {
			const s = await api.createSession();
			localStorage.setItem(SESSION_KEY, s.id);
			snap = s;
			selectedCarId = s.cars[0]?.id ?? null;
			error = null;
		} catch (e) {
			error = e instanceof ApiError ? `${e.message} (${e.code})` : String(e);
		}
	}

	async function resume() {
		const id = localStorage.getItem(SESSION_KEY);
		if (!id) return;
		try {
			snap = await api.session(id);
			selectedCarId = snap.cars[0]?.id ?? null;
		} catch {
			// Stale id (deleted save): fall through to the start screen.
			localStorage.removeItem(SESSION_KEY);
		}
	}

	/** Leave the current city and return to the picker. The save stays on disk. */
	async function leave() {
		if (snap) {
			// Persist before walking away so nothing since the last tick is lost.
			try {
				await api.saveSession(snap.id);
			} catch {
				/* best effort: the server also saves on shutdown */
			}
		}
		localStorage.removeItem(SESSION_KEY);
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
		if (!snap || !selectedCar) return;
		const id = snap.id;
		const carId = selectedCar.id;
		busy = true;
		try {
			await api.moveCar(id, carId, dir);
			// Two round trips, so a poll may land between them; only the final
			// snapshot is authoritative, and version bump discards stale polls.
			snap = await api.tick(id, 1);
			version++;
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
		if (!snap || !selectedCar) return;
		void apply(() => api.moveCar(snap!.id, selectedCar!.id, dir));
	}

	function step(n = 1) {
		if (!snap) return;
		void apply(() => api.tick(snap!.id, n));
	}

	function setSpeed(speed: number) {
		if (!snap) return;
		void apply(() => api.setSpeed(snap!.id, speed));
	}

	function assign(orderId: string) {
		if (!snap || !selectedCar) return;
		void apply(() => api.assignOrder(snap!.id, orderId, selectedCar!.id));
	}

	function unassign(orderId: string) {
		if (!snap) return;
		void apply(() => api.unassignOrder(snap!.id, orderId));
	}

	function tow(carId: string) {
		if (!snap) return;
		void apply(() => api.towCar(snap!.id, carId));
	}

	function buy(cls: string) {
		if (!snap) return;
		void apply(() => api.buyCar(snap!.id, cls));
	}

	function build(buildId: string) {
		if (!snap) return;
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
		if (!snap) return;
		const target = e.target as HTMLElement | null;
		// Never hijack typing in a form control.
		if (target && /^(INPUT|TEXTAREA|SELECT)$/.test(target.tagName)) return;

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
		if (e.key === 'Tab') {
			e.preventDefault();
			cycleCar(e.shiftKey ? -1 : 1);
			return;
		}
		if (e.key === 'p' || e.key === 'P') {
			setSpeed(snap.paused ? 1 : 0);
		}
	}

	// --- polling -------------------------------------------------------------

	let timer: ReturnType<typeof setTimeout> | undefined;
	let pollingActive = false;
	/** Guards against overlapping polls piling up behind a slow request. */
	let polling = false;

	/**
	 * Bumped whenever this tab applies a snapshot it caused. A poll that
	 * started before a mutation must not overwrite the newer state when it
	 * lands, or the UI flickers backwards for one interval.
	 */
	let version = 0;

	/**
	 * The server owns the clock, so the client polls rather than simulating.
	 *
	 * Poll whenever a session is open, paused or not. A paused session still
	 * changes under this tab: the CLI, an agent or a second browser can step
	 * it, and those mutations return their snapshot to the caller only. Gating
	 * on `paused` left this tab frozen at its own last read.
	 *
	 * A paused session changes only on outside input, so it is polled at a
	 * slower cadence: fast enough to feel live, cheap enough for a dashboard
	 * left open on a still city.
	 */
	function startPolling() {
		pollingActive = true;
		schedule(0);
	}

	function schedule(delay: number) {
		if (timer) clearTimeout(timer);
		if (!pollingActive) return;
		timer = setTimeout(async () => {
			await poll();
			schedule(snap?.paused ? PAUSED_POLL_MS : RUNNING_POLL_MS);
		}, delay);
	}

	async function poll() {
		if (!snap || busy || polling) return;
		// A hidden tab needs no fresh pixels; the next tick runs on return.
		if (typeof document !== 'undefined' && document.hidden) return;

		const id = snap.id;
		const startedAt = version;
		polling = true;
		try {
			const next = await api.session(id);
			// Discard if the player left the city or switched, or if a local
			// mutation has already applied something newer.
			if (snap?.id === id && version === startedAt) snap = next;
		} catch {
			// Transient: the next poll retries.
		} finally {
			polling = false;
		}
	}

	function stopPolling() {
		pollingActive = false;
		if (timer) clearTimeout(timer);
		timer = undefined;
	}

	onMount(() => {
		void resume();
		void loadSaves();
		startPolling();
		window.addEventListener('keydown', onKey);
	});

	onDestroy(() => {
		stopPolling();
		window.removeEventListener('keydown', onKey);
	});
</script>

<svelte:head>
	<title>DrBrain Delivery</title>
</svelte:head>

{#if !snap}
	<main class="flex h-full items-start justify-center overflow-y-auto p-6">
		<div class="w-full max-w-lg">
			<h1 class="text-2xl font-semibold text-fog-300">DrBrain Delivery</h1>
			<p class="mt-2 text-sm text-fog-400">
				One city that grows as you deliver. Drive the cars yourself, one step at a time.
			</p>

			<button
				type="button"
				class="mt-6 w-full rounded bg-[var(--color-money)]/15 px-4 py-2.5 text-sm font-medium text-[var(--color-money)] transition-colors hover:bg-[var(--color-money)]/25"
				onclick={startSession}
			>
				Start a new city
			</button>

			<h2 class="mt-8 text-xs font-semibold tracking-wide text-fog-400 uppercase">
				Your cities
				{#if savesLoading}
					<span class="ml-1 font-normal normal-case text-fog-500">loading…</span>
				{/if}
			</h2>

			{#if savesLoading}
				<ul class="mt-2 space-y-1.5">
					{#each [0, 1] as i (i)}
						<li class="h-12 animate-pulse rounded-lg border border-ink-700 bg-ink-800/50"></li>
					{/each}
				</ul>
			{:else if saves.length === 0}
				<div class="mt-2 rounded-lg border border-dashed border-ink-700 px-3 py-4 text-center">
					<p class="text-xs text-fog-400">No saved cities yet.</p>
					<p class="mt-1 text-xs text-fog-500">Start a new city to begin.</p>
				</div>
			{:else}
				<ul class="mt-2 divide-y divide-ink-800 rounded-lg border border-ink-700 bg-ink-900">
					{#each saves as s (s.id)}
						<li>
							<button
								type="button"
								class="group flex w-full items-center justify-between gap-3 px-3 py-2.5 text-left transition-colors hover:bg-ink-800"
								onclick={() => openSave(s.id)}
								title={`Open ${s.id}`}
							>
								<span class="min-w-0">
									<span class="block truncate text-sm text-fog-300">{s.id}</span>
									<span class="text-xs text-fog-500">
										tick {s.tick} · {s.deliveries} delivered · rep {s.reputation.toFixed(2)}
									</span>
								</span>
								<span class="flex shrink-0 items-center gap-2">
									<span class="text-sm text-[var(--color-money)]">${s.money}</span>
									<span
										class="rounded border border-ink-600 px-2 py-0.5 text-xs text-fog-400 transition-colors group-hover:border-[var(--color-charger)] group-hover:text-[var(--color-charger)]"
									>
										Open
									</span>
								</span>
							</button>
						</li>
					{/each}
				</ul>
			{/if}

			{#if error}
				<p class="mt-4 text-xs text-[var(--color-danger)]">{error}</p>
			{/if}
		</div>
	</main>
{:else}
	<div class="flex h-full flex-col">
		<!-- top bar -->
		<header class="flex flex-wrap items-center gap-x-6 gap-y-1 border-b border-ink-600 bg-ink-900 px-4 py-2">
			<h1 class="text-sm font-semibold tracking-wide text-fog-300">DrBrain Delivery</h1>

			<div class="flex items-center gap-4 text-xs">
				<span class="text-fog-500">tick <span class="text-fog-300">{snap.tick}</span></span>
				<span class="text-[var(--color-money)]">${snap.ledger.money}</span>
				{#if snap.ledger.upkeepDue > 0}
					<!--
						The running cost has to be visible or it is just a mysterious drop
						in the money number. Shown as "due", not "paid", so the player can
						act on it (park the car) before the charge lands.
					-->
					<span
						class="text-fog-500"
						data-upkeep-due={snap.ledger.upkeepDue}
						title="Fleet upkeep charged next interval. Park cars on a depot tile to stop it."
					>
						−<span class="text-[var(--color-warn)]">${snap.ledger.upkeepDue}</span>/ivl
					</span>
				{/if}
				<span class="text-fog-500">
					rep <span class="text-fog-300">{snap.ledger.reputation.toFixed(2)}</span>
				</span>
				<span class="text-fog-500">
					deliveries <span class="text-fog-300">{snap.ledger.deliveries}</span>
				</span>
				{#if snap.ledger.onTimeStreak > 0}
					<span class="text-[var(--color-money)]">streak {snap.ledger.onTimeStreak}</span>
				{/if}
			</div>

			<div class="ml-auto flex items-center gap-2">
				<button
					type="button"
					class="rounded px-2 py-1 text-xs text-fog-500 hover:text-fog-300"
					onclick={leave}
					title="Save and return to the city picker"
				>
					cities
				</button>
				<span class="h-4 w-px bg-ink-700"></span>
				{#each SPEEDS as s (s.value)}
					<button
						type="button"
						class="rounded px-2 py-1 text-xs transition-colors {snap.speed === s.value
							? 'bg-[var(--color-charger)]/20 text-[var(--color-charger)]'
							: 'text-fog-500 hover:text-fog-300'}"
						onclick={() => setSpeed(s.value)}
						aria-pressed={snap.speed === s.value}
						title={s.value === 0 ? 'Pause the clock' : `Run at ${s.value}× speed`}
					>
						{s.value === 0 ? (snap.paused ? 'paused' : 'pause') : s.label}
					</button>
				{/each}
				<span class="mx-1 h-4 w-px bg-ink-700"></span>
				<button
					type="button"
					class="rounded border border-ink-600 px-2 py-1 text-xs text-fog-400 hover:text-fog-300"
					onclick={() => step(1)}>step</button
				>
				<button
					type="button"
					class="rounded border border-ink-600 px-2 py-1 text-xs text-fog-400 hover:text-fog-300"
					onclick={() => step(10)}>+10</button
				>
			</div>
		</header>

		{#if error}
			<div
				class="border-b border-[var(--color-danger)]/40 bg-[var(--color-danger)]/10 px-4 py-1.5 text-xs text-[var(--color-danger)]"
			>
				{error}
			</div>
		{/if}

		<!-- body -->
		<div
			class="grid min-h-0 flex-1 grid-cols-1 gap-3 overflow-hidden px-3 pt-3 pb-5 lg:grid-cols-[minmax(0,1fr)_360px]"
		>
			<div class="flex min-h-0 flex-col gap-3 overflow-y-auto">
				<!-- The map panel sizes to its content instead of stretching, so
				     the square grid does not float in a wide empty box. -->
				<div class="w-fit self-center">
					<Map
						city={snap.city}
						cars={snap.cars}
						selectedCarId={selectedCar?.id ?? null}
						highlightKitchen={highlightedOrder?.kitchenId ?? null}
						highlightCustomer={highlightedOrder?.customerId ?? null}
						closures={snap.closures}
						onSelectCar={(id) => (selectedCarId = id)}
					/>
				</div>

				<div class="rounded-lg border border-ink-600 bg-ink-900">
					<header class="border-b border-ink-700 px-3 py-2">
						<h2 class="text-xs font-semibold tracking-wide text-fog-400 uppercase">Controls</h2>
					</header>
					<div class="flex flex-wrap items-center gap-x-4 gap-y-2 px-3 py-2 text-xs text-fog-400">
						<!-- A D-pad, because keyboard-only excludes mouse and touch. -->
						<div class="grid grid-cols-3 gap-1" role="group" aria-label="Drive">
							<span></span>
							<button
								type="button"
								class="rounded border border-ink-600 px-2 py-1 text-fog-300 hover:border-[var(--color-charger)] hover:text-[var(--color-charger)]"
								onclick={() => drive('NORTH')}
								aria-label="Move north"
								title="Move north (w)">↑</button
							>
							<span></span>
							<button
								type="button"
								class="rounded border border-ink-600 px-2 py-1 text-fog-300 hover:border-[var(--color-charger)] hover:text-[var(--color-charger)]"
								onclick={() => drive('WEST')}
								aria-label="Move west"
								title="Move west (a)">←</button
							>
							<button
								type="button"
								class="rounded border border-ink-600 px-2 py-1 text-fog-300 hover:border-[var(--color-charger)] hover:text-[var(--color-charger)]"
								onclick={() => step(1)}
								aria-label="Advance one tick"
								title="Advance one tick (space)">•</button
							>
							<button
								type="button"
								class="rounded border border-ink-600 px-2 py-1 text-fog-300 hover:border-[var(--color-charger)] hover:text-[var(--color-charger)]"
								onclick={() => drive('EAST')}
								aria-label="Move east"
								title="Move east (d)">→</button
							>
							<span></span>
							<button
								type="button"
								class="rounded border border-ink-600 px-2 py-1 text-fog-300 hover:border-[var(--color-charger)] hover:text-[var(--color-charger)]"
								onclick={() => drive('SOUTH')}
								aria-label="Move south"
								title="Move south (s)">↓</button
							>
							<span></span>
						</div>

						<div class="flex flex-col gap-0.5">
							<span><kbd class="text-fog-300">w a s d</kbd> drive</span>
							<span><kbd class="text-fog-300">shift</kbd> + direction: queue only</span>
							<span><kbd class="text-fog-300">space</kbd> tick only</span>
							<span><kbd class="text-fog-300">tab</kbd> switch car</span>
							<span><kbd class="text-fog-300">p</kbd> pause/resume</span>
						</div>
					</div>
					{#if selectedCar}
						<div class="border-t border-ink-800 px-3 py-1.5 text-xs text-fog-400">
							driving <span class="text-fog-300">{selectedCar.name}</span>
							{#if selectedCar.batteryPct <= 25}
								<span class="text-[var(--color-warn)]"> — low battery</span>
							{/if}
						</div>
					{/if}
				</div>

				<div class="rounded-lg border border-ink-600 bg-ink-900">
					<header class="flex items-center justify-between border-b border-ink-700 px-3 py-2">
						<h2 class="text-xs font-semibold tracking-wide text-fog-400 uppercase">Activity</h2>
						<button
							type="button"
							class="text-xs text-fog-500 hover:text-fog-300"
							onclick={() => (feedOpen = !feedOpen)}
						>
							{feedOpen ? 'hide' : 'show'}
						</button>
					</header>
					{#if feedOpen}
						<ul class="max-h-44 overflow-y-auto px-3 py-1">
							{#each feed as e, i (i)}
								<li class="py-0.5 text-xs text-fog-400">
									<span class="text-fog-500">t{e.tick}</span>
									{e.message}
								</li>
							{/each}
							{#if feed.length === 0}
								<li class="py-0.5 text-xs text-fog-500">Nothing has happened yet.</li>
							{/if}
						</ul>
					{/if}
				</div>
			</div>

			<div class="flex min-h-0 flex-col gap-3 overflow-y-auto">
				<FleetPanel
					cars={snap.cars}
					selectedCarId={selectedCar?.id ?? null}
					onSelect={(id) => (selectedCarId = id)}
					onTow={tow}
					{buyable}
					onBuy={buy}
				/>
				<OrdersPanel
					orders={snap.orders}
					selectedCarId={selectedCar?.id ?? null}
					selectedOrderId={highlightedOrder?.id ?? null}
					onAssign={assign}
					onUnassign={unassign}
					onSelectOrder={(id) => (selectedOrderId = id)}
				/>
				<ProgressPanel
					achievements={snap.achievements}
					unlocks={snap.unlocks}
					builds={snap.builds}
					onBuild={build}
				/>
			</div>
		</div>
	</div>
{/if}
