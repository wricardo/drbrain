<script lang="ts">
	import { onDestroy, onMount, untrack } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { api, ApiError, SNAPSHOT_FIELDS, type CityInfo, type Direction, type SessionSummary, type Snapshot } from '$lib/delivery/api';
	import { canPlayCity, sortCities, statusLabel } from '$lib/delivery/cities';
	import { pageTitle } from '$lib/delivery/brand';
	import { subscribeToSession } from '$lib/delivery/subscribe';
	import { forgetSession, ownerTokenFor, rememberSession } from '$lib/delivery/ownerTokens';
	import ClaimBanner from '$lib/delivery/components/ClaimBanner.svelte';
	import Map from '$lib/delivery/Map.svelte';
	import Panel from '$lib/delivery/Panel.svelte';
	import FleetPanel from '$lib/delivery/FleetPanel.svelte';
	import LedgerPanel from '$lib/delivery/LedgerPanel.svelte';
	import MonthlyPanel from '$lib/delivery/MonthlyPanel.svelte';
	import OrdersPanel from '$lib/delivery/OrdersPanel.svelte';
	import Hero from '@drbrain/ui/components/Hero.svelte';
	import Button from '@drbrain/ui/components/Button.svelte';
	import EmptyState from '@drbrain/ui/components/EmptyState.svelte';
	import Skeleton from '@drbrain/ui/components/Skeleton.svelte';
	import GrowthPanel from '$lib/delivery/GrowthPanel.svelte';
	import AiPromptCard from '@drbrain/ui/components/AiPromptCard.svelte';
	import { buildSessionPrompt } from '$lib/delivery/prompt';

	// --- state ---------------------------------------------------------------

	let snap = $state<Snapshot | null>(null);
	let error = $state<string | null>(null);
	let selectedCarId = $state<string | null>(null);
	/** Which order's route is highlighted on the map. */
	let selectedOrderId = $state<string | null>(null);
	let busy = $state(false);
	/** When true the D-pad only queues a step; the clock advances via step. */
	let queueOnly = $state(false);

	/** False when the open session belongs to someone else: the page then only watches. */
	let canPlay = $state(true);

	/** Saves on disk, shown on the start screen. */
	let saves = $state<SessionSummary[]>([]);
	let savesLoading = $state(true);
	let savesFailed = $state(false);
	let opening = $state(false);

	/** City catalogue for the start-screen picker. */
	let cities = $state<CityInfo[]>([]);
	let citiesLoading = $state(true);
	let citiesFailed = $state(false);
	let selectedCityId = $state('city');

	const origin = typeof window !== 'undefined' ? window.location.origin : '';
	const aiPrompt = $derived(
		snap && canPlay
			? buildSessionPrompt(origin, snap, {
					ownerToken: ownerTokenFor(snap.id),
					canPlay
				})
			: ''
	);

	const selectedCity = $derived(cities.find((c) => c.id === selectedCityId) ?? null);

	async function loadCities() {
		citiesLoading = true;
		citiesFailed = false;
		try {
			cities = sortCities(await api.cities());
			const stillThere = cities.some((c) => c.id === selectedCityId);
			if (!stillThere) {
				selectedCityId =
					cities.find((c) => c.id === 'city' && canPlayCity(c))?.id ??
					cities.find((c) => canPlayCity(c))?.id ??
					'';
			}
		} catch {
			citiesFailed = true;
			cities = [];
			selectedCityId = '';
		} finally {
			citiesLoading = false;
		}
	}

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

	/** Cosmetic name for a save: owner's city, else a short id suffix. */
	function cityName(s: SessionSummary): string {
		return s.ownerDisplayName !== 'Guest' ? `${s.ownerDisplayName}’s city` : `City …${s.id.slice(-6)}`;
	}

	/** The picker entry for the open city, once the saves list has loaded. */
	const openEntry = $derived(saves.find((s) => s.id === snap?.id) ?? null);

	/**
	 * Display name for the open city.
	 *
	 * Ownership resolves in the same order as the picker: the server's `mine`
	 * flag, then the owner's display name, then this browser's owner token
	 * (which is what makes an unclaimed city playable), and finally an id
	 * suffix so a stranger's guest city still has a stable label.
	 */
	const openName = $derived.by(() => {
		if (!snap) return '';
		if (openEntry?.mine) return 'Your city';
		if (openEntry && openEntry.ownerDisplayName !== 'Guest') {
			return `${openEntry.ownerDisplayName}’s city`;
		}
		if (ownerTokenFor(snap.id)) return 'Your city';
		return `City …${snap.id.slice(-6)}`;
	});

	/** Spectator copy: name a real player's city, keep a stranger's guest city generic. */
	const spectatorText = $derived(
		openName === 'Your city' || openName.startsWith('City …')
			? 'Viewing a city — read-only'
			: `Viewing ${openName} — read-only`
	);

	/** The open city lives in the URL (?city=<id>) so back, refresh and links work. */
	const cityParam = $derived(page.url.searchParams.get('city'));

	function openSave(id: string) {
		if (opening) return;
		void goto(`?city=${encodeURIComponent(id)}`, { noScroll: true });
	}

	async function loadCity(id: string) {
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
			void goto('?', { replaceState: true, noScroll: true });
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

	const assignedCarIds = $derived.by(() => {
		const ids = new Set<string>();
		for (const order of snap?.orders ?? []) {
			if (order.state === 'ASSIGNED' && order.assignedCar) ids.add(order.assignedCar);
		}
		return ids;
	});

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

	async function startSession(cityId?: string) {
		if (opening) return;
		opening = true;
		try {
			const s = await api.createSession(undefined, undefined, cityId || undefined);
			canPlay = true;
			snap = s;
			selectedCarId = s.cars[0]?.id ?? null;
			error = null;
			void goto(`?city=${encodeURIComponent(s.id)}`, { noScroll: true });
		} catch (e) {
			error = e instanceof ApiError ? `${e.message} (${e.code})` : String(e);
		} finally {
			opening = false;
		}
	}

	function startSelectedCity() {
		void startSession(selectedCityId || undefined);
	}

	/** Back to the city picker: navigating drops ?city, and the effect below closes the city. */
	function leave() {
		void goto('?', { noScroll: true });
	}

	/** Close the open city. The save stays on disk. */
	async function closeCity() {
		const cur = snap;
		if (cur && canPlay) {
			// Persist before walking away so nothing since the last tick is lost.
			try {
				await api.saveSession(cur.id);
			} catch {
				/* best effort: the server also saves on shutdown */
			}
		}
		if (snap?.id !== cur?.id) return; // another city opened meanwhile
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
			// Direct driving is "move this car now": a leftover queue from
			// queue-only mode would otherwise make the tick execute the stale
			// head instead of the direction just pressed.
			if (selectedCar.queuedMoves.length > 0) await api.clearMoves(id, carId);
			await api.moveCar(id, carId, dir);
			// More than one round trip, so a frame may describe the state in
			// between; only the tick's own response is authoritative for this step.
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
		// Editing fields own every key. Everything else does not: selecting a car
		// leaves focus on its map or fleet button, and driving from there is the
		// normal way to play, so buttons and links must not swallow the keys.
		if (target?.closest('input, textarea, select, [contenteditable]')) return;
		const onControl = target?.closest('button, a') !== null;

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
			// Space and Enter belong to the focused control: pressing them while
			// a button has focus must activate that button, not also advance the
			// clock.
			if (onControl) return;
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

	// URL is the source of truth for which city is open.
	$effect(() => {
		const c = cityParam;
		untrack(() => {
			if (c) {
				if (snap?.id !== c) void loadCity(c);
			} else if (snap) {
				void closeCity();
			}
		});
	});

	onMount(() => {
		void loadSaves();
		void loadCities();
		window.addEventListener('keydown', onKey);
	});

	onDestroy(() => {
		stopStream();
		window.removeEventListener('keydown', onKey);
	});
</script>

<svelte:head>
	<title>{snap ? pageTitle(openName) : pageTitle()}</title>
</svelte:head>

{#if cityParam && !snap && opening}
	<div class="db-container flex flex-col gap-3 py-4">
		<Skeleton label="Opening city…" />
	</div>
{:else if !snap}
	<ClaimBanner />
	<div class="db-container game-landing">
		<Hero
			label="Delivery"
			title="Run the fleet."
			accent="Grow the city."
			description="Drive a delivery van, pick up orders and earn enough to expand your fleet. The city grows as you deliver."
			image="/art/delivery.webp"
			wideArt
		>
			<div class="flex flex-col gap-3">
				<div class="flex flex-col gap-3 sm:flex-row sm:items-end">
					<label class="block min-w-0 flex-1" for="city-select">
						<span class="text-xs font-semibold uppercase tracking-wide text-muted">City</span>
						{#if citiesLoading}
							<Skeleton label="Loading cities…" />
						{:else if citiesFailed}
							<span class="mt-1 flex items-center gap-2 text-sm text-muted">
								Catalogue unavailable.
								<Button variant="secondary" onclick={loadCities}>Retry</Button>
							</span>
						{:else if cities.length === 0}
							<span class="mt-1 block text-sm text-muted">Using the server's default city.</span>
						{:else}
							<select id="city-select" class="db-input mt-1 max-w-md" bind:value={selectedCityId}>
								{#each cities as c (c.id)}
									<option value={c.id}>
										{c.name} · {statusLabel(c.status)}{c.ownedByMe ? ' · yours' : ''}
									</option>
								{/each}
							</select>
						{/if}
					</label>
					<div class="flex flex-wrap gap-2">
						<Button disabled={opening || citiesLoading} onclick={startSelectedCity} data-testid="start-city">
							{opening
								? 'Opening city…'
								: selectedCity
									? `Play ${selectedCity.name}`
									: 'Start the default city'}
							<span aria-hidden="true">→</span>
						</Button>
						<Button href="/delivery/cities" variant="secondary">Browse cities</Button>
					</div>
				</div>
				{#if selectedCity}
					<p class="text-xs text-muted">
						{selectedCity.gridSize} × {selectedCity.gridSize} tiles · {statusLabel(selectedCity.status)}{selectedCity.ownedByMe
							? ' · your draft'
							: ''}
					</p>
				{/if}
			</div>
		</Hero>
		{#if error}<p class="mt-4 rounded-lg border border-[var(--db-danger)]/40 bg-[var(--db-danger)]/10 px-4 py-3 text-sm text-[var(--db-danger)]" role="alert">{error}</p>{/if}
		<section class="py-8" aria-labelledby="saved-cities">
			<h2 id="saved-cities" class="mb-3 text-2xl font-bold tracking-tight">Saved cities{saves.length > 0 ? ` (${saves.length})` : ''}</h2>
			{#if savesLoading}
				<Skeleton label="Loading cities…" />
			{:else if savesFailed}
				<EmptyState title="Could not load cities" description="Try loading the cities again.">
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
										{s.mine || ownerTokenFor(s.id) ? 'Your city' : cityName(s)}
										{#if s.mine || ownerTokenFor(s.id)}
											<span class="ml-1 rounded bg-[var(--db-accent)]/15 px-1.5 py-0.5 text-[11px] font-semibold text-[var(--db-accent)]">yours</span>
										{/if}
									</span>
									{#if !(s.mine || ownerTokenFor(s.id))}
										<span class="block truncate text-[11px] text-faint">{s.id}</span>
									{/if}
									<span class="text-xs text-faint">
										tick {s.tick} · {s.deliveries} delivered · rep {s.reputation.toFixed(2)}
									</span>
								</span>
								<span class="flex shrink-0 items-center gap-2">
									<span class="text-sm text-[var(--color-money)]">${s.money}</span>
									<span
										class="rounded border border-edge-strong px-2 py-0.5 text-xs text-muted transition-colors group-hover:border-[var(--db-accent)] group-hover:text-[var(--db-accent)]"
									>
										Open
									</span>
								</span>
							</button>
							<!-- Outside the button: a link inside a button is invalid HTML. -->
							{#if s.ownerHandle && !s.mine && !ownerTokenFor(s.id)}
								<a href={`/u/${s.ownerHandle}`} class="-mt-1.5 inline-flex min-h-11 items-center px-4 text-xs text-muted hover:text-body">@{s.ownerHandle}</a>
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
			<span class="text-sm text-faint" data-testid="open-city-name">{openName}</span>
			<div class="flex flex-wrap items-center gap-x-4 gap-y-1 text-sm tabular-nums">
				<span class="text-faint">Tick <strong class="text-body">{snap.tick}</strong></span>
				<span class="text-[var(--color-money)]">${snap.ledger.money}</span>
				<span class="text-faint">
					Reputation <span class="text-body">{snap.ledger.reputation.toFixed(2)}</span>
					<span
						class="text-faint"
						title="Share of each quoted reward this standing collects. Reputation rises 0.02 per on-time delivery and falls 0.10 per expiry."
					>
						· pay <span class="text-body">{Math.round(snap.ledger.payMultiplier * 100)}%</span>
					</span>
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
					← Saved cities
				</button>
				{#if canPlay}
					<span class="hidden h-4 w-px bg-track sm:block"></span>
					{#each SPEEDS as s (s.value)}
						<button
							type="button"
							class="min-h-11 min-w-11 rounded-lg px-2 text-sm font-semibold transition-colors {snap.speed === s.value
								? 'bg-[var(--db-accent)]/15 text-[var(--db-accent)]'
								: 'text-faint hover:text-body'}"
							onclick={() => setSpeed(s.value)}
							aria-pressed={snap.speed === s.value}
							title={s.value === 0 ? 'Pause the clock' : `Run at ${s.value}× speed`}
						>
							{s.value === 0 ? (snap.paused ? 'paused' : 'pause') : s.label}
						</button>
					{/each}
					<span class="mx-1 hidden h-4 w-px bg-track sm:block"></span>
					<button
						type="button"
						class="min-h-11 min-w-11 rounded-lg border border-edge-strong px-2.5 text-sm font-semibold text-muted hover:bg-surface-muted hover:text-body"
						onclick={() => step(1)}>step</button
					>
					<button
						type="button"
						class="min-h-11 min-w-11 rounded-lg border border-edge-strong px-2.5 text-sm font-semibold text-muted hover:bg-surface-muted hover:text-body"
						onclick={() => step(10)}>+10</button
					>
				{/if}
			</div>
		</header>

		{#if !canPlay}
			<p class="rounded-lg border border-edge bg-surface-muted px-4 py-2 text-sm text-muted" role="status" data-testid="spectator-notice">
				{spectatorText}
			</p>
		{/if}

		{#if error}
			<div
				class="rounded-lg border border-[var(--db-danger)]/40 bg-[var(--db-danger)]/10 px-4 py-2 text-sm text-[var(--db-danger)]" role="alert"
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
						orders={snap.orders}
						{assignedCarIds}
						selectedCarId={selectedCar?.id ?? null}
						highlightKitchen={highlightedOrder?.kitchenId ?? null}
						highlightCustomer={highlightedOrder?.customerId ?? null}
						closures={snap.closures}
						{moveMs}
						onSelectCar={(id) => (selectedCarId = id)}
					/>
				</div>

				{#if canPlay}
					<Panel
						title="Controls"
						summary={selectedCar
							? `driving ${selectedCar.name}${queueOnly ? ' · queueing — press step to advance' : ''}`
							: ''}
					>
						<div class="flex flex-wrap items-center gap-x-4 gap-y-2 px-3 py-2 text-xs text-muted">
							<!-- A D-pad, because keyboard-only excludes mouse and touch. -->
							<div class="grid grid-cols-3 gap-1" role="group" aria-label="Drive">
								<span></span>
								<button
									type="button"
									class="size-11 rounded-lg border border-edge-strong text-base font-semibold text-body hover:border-[var(--db-accent)] hover:text-[var(--db-accent)]"
									onclick={() => (queueOnly ? queueStep('NORTH') : drive('NORTH'))}
									aria-label="Move north"
									title={queueOnly ? 'Queue north (w)' : 'Move north (w)'}>↑</button
								>
								<span></span>
								<button
									type="button"
									class="size-11 rounded-lg border border-edge-strong text-base font-semibold text-body hover:border-[var(--db-accent)] hover:text-[var(--db-accent)]"
									onclick={() => (queueOnly ? queueStep('WEST') : drive('WEST'))}
									aria-label="Move west"
									title={queueOnly ? 'Queue west (a)' : 'Move west (a)'}>←</button
								>
								<button
									type="button"
									class="size-11 rounded-lg border border-edge-strong text-base font-semibold text-body hover:border-[var(--db-accent)] hover:text-[var(--db-accent)]"
									onclick={() => step(1)}
									aria-label="Advance one tick"
									title="Advance one tick (space)">•</button
								>
								<button
									type="button"
									class="size-11 rounded-lg border border-edge-strong text-base font-semibold text-body hover:border-[var(--db-accent)] hover:text-[var(--db-accent)]"
									onclick={() => (queueOnly ? queueStep('EAST') : drive('EAST'))}
									aria-label="Move east"
									title={queueOnly ? 'Queue east (d)' : 'Move east (d)'}>→</button
								>
								<span></span>
								<button
									type="button"
									class="size-11 rounded-lg border border-edge-strong text-base font-semibold text-body hover:border-[var(--db-accent)] hover:text-[var(--db-accent)]"
									onclick={() => (queueOnly ? queueStep('SOUTH') : drive('SOUTH'))}
									aria-label="Move south"
									title={queueOnly ? 'Queue south (s)' : 'Move south (s)'}>↓</button
								>
								<span></span>
							</div>

							<button
								type="button"
								class="min-h-11 rounded-lg border px-2.5 text-sm font-semibold transition-colors {queueOnly
									? 'border-[var(--db-accent)]/40 bg-[var(--db-accent)]/15 text-[var(--db-accent)]'
									: 'border-edge-strong text-muted hover:bg-surface-muted hover:text-body'}"
								aria-pressed={queueOnly}
								onclick={() => (queueOnly = !queueOnly)}
								title="Queue a step without advancing the clock"
							>
								Queue only
							</button>

							<div class="flex flex-col gap-0.5">
								<span><kbd class="text-body">w a s d</kbd> drive</span>
								<span><kbd class="text-body">shift</kbd> + direction: queue only</span>
								<span>toggle: tap to queue, step to advance</span>
								<span><kbd class="text-body">space</kbd> tick only</span>
								<span><kbd class="text-body">c</kbd> switch car</span>
								<span><kbd class="text-body">p</kbd> pause/resume</span>
							</div>
						</div>
						{#if selectedCar}
							<div class="border-t border-edge px-3 py-1.5 text-xs text-muted">
								driving <span class="text-body">{selectedCar.name}</span>
								{#if selectedCar.queuedMoves.length > 0}
									<span class="text-faint"> — {selectedCar.queuedMoves.length} queued</span>
								{/if}
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
				<OrdersPanel
					orders={snap.orders}
					selectedCarId={selectedCar?.id ?? null}
					selectedCarCapacity={selectedCar?.capacity ?? null}
					selectedOrderId={highlightedOrder?.id ?? null}
					onAssign={canPlay ? assign : undefined}
					onUnassign={canPlay ? unassign : undefined}
					onSelectOrder={(id) => (selectedOrderId = id)}
				/>
				<FleetPanel
					cars={snap.cars}
					upkeepDue={snap.ledger.upkeepDue}
					{assignedCarIds}
					selectedCarId={selectedCar?.id ?? null}
					onSelect={(id) => (selectedCarId = id)}
					onTow={canPlay ? tow : undefined}
					buyable={canPlay ? buyable : []}
					onBuy={canPlay ? buy : undefined}
				/>
				<MonthlyPanel months={snap.ledger.months} ticksPerMonth={snap.ledger.ticksPerMonth} />
				<LedgerPanel entries={snap.ledger.cashEntries} money={snap.ledger.money} />
				{#if canPlay}
					<AiPromptCard
						prompt={aiPrompt}
						tone="delivery"
						blurb={`Copy this prompt into an AI chat to let it run city ${snap.id}.`}
					/>
				{/if}
				<GrowthPanel growth={snap.growth} tick={snap.tick} />
			</div>
		</div>
	</div>
{/if}
