<script lang="ts">
	import type { CarView, CityView, ClosureView, OrderView } from '$lib/delivery/api';
	import { carGlyphStyle, carIndex, glyphStyle } from '$lib/delivery/glyphs';
	import MapIcon from '$lib/delivery/MapIcon.svelte';

	interface Props {
		city: CityView;
		assignedCarIds: ReadonlySet<string>;
		cars: CarView[];
		orders: OrderView[];
		selectedCarId: string | null;
		/** Kitchen and customer of the order being highlighted, if any. */
		highlightKitchen?: string | null;
		highlightCustomer?: string | null;
		/** Street works currently closed. The rows already draw them as 'X'. */
		closures?: ClosureView[];
		/**
		 * How long a car takes to glide from the tile it was on to the tile it
		 * is on now, in milliseconds.
		 *
		 * A running clock passes its tick length, so a car arrives just as the
		 * next snapshot lands and the motion is continuous. A paused clock
		 * passes a fixed step duration: sessions start paused and manual driving
		 * is the normal case, so 0 here would teleport a car a whole tile on
		 * every keystroke. Only reduced-motion legitimately reaches 0, and
		 * app.css already forces transitions to zero for that.
		 */
		moveMs?: number;
		onSelectCar?: (id: string) => void;
	}

	let {
		city,
		assignedCarIds,
		cars,
		orders,
		selectedCarId,
		highlightKitchen = null,
		highlightCustomer = null,
		closures = [],
		moveMs = 0,
		onSelectCar
	}: Props = $props();

	/**
	 * Tile size adapts to the viewport so the whole dashboard fits without the
	 * player having to scroll to see their own car and controls at once. It is
	 * clamped so a very small window does not make tiles unreadable and a huge
	 * one does not push everything else off screen.
	 */
	let viewportH = $state(900);
	let viewportW = $state(1440);

	const TILE = $derived.by(() => {
		const hudSpace = 415;
		const sidePanelSpace = viewportW >= 1024 ? 465 : 68;
		const byHeight = Math.floor((viewportH - hudSpace) / Math.max(city.gridSize, 1));
		const byWidth = Math.floor((viewportW - sidePanelSpace) / Math.max(city.gridSize, 1));
		return Math.max(14, Math.min(32, byHeight, byWidth));
	});

	const rows = $derived(city.rows);

	// Only mark straight, uninterrupted road segments; intersections stay clear.
	function roadAt(x: number, y: number): boolean {
		return rows[y]?.[x] === '.';
	}

	function roadDirection(x: number, y: number): string {
		const north = roadAt(x, y - 1);
		const south = roadAt(x, y + 1);
		const east = roadAt(x + 1, y);
		const west = roadAt(x - 1, y);
		if (north && south && !east && !west) return 'road-vertical';
		if (east && west && !north && !south) return 'road-horizontal';
		return '';
	}

	/**
	 * Cars grouped by tile.
	 *
	 * A tile is not limited to one car any more: a depot holds any number,
	 * and a charger holds its own capacity.
	 * Rows can only draw one digit per tile, so a plain `map.set` per car
	 * silently made every car but the last unselectable — the glyph and the
	 * click target also disagreed about which car they meant.
	 */
	const carsAt = $derived.by(() => {
		const map = new Map<string, CarView[]>();
		for (const c of cars) {
			const k = `${c.x},${c.y}`;
			const list = map.get(k);
			if (list) list.push(c);
			else map.set(k, [c]);
		}
		return map;
	});

	/**
	 * The move each car is currently animating: where it started and how long
	 * that move lasts.
	 *
	 * Deliberately non-reactive, and updated in an $effect rather than inside
	 * `drawn`. Writing to it from the derivation made the derivation depend on
	 * its own previous write: the second time Svelte evaluated it — which it may
	 * do more than once per change — every car looked stationary and every glide
	 * collapsed to zero.
	 *
	 * `ms` is stored, not recomputed, and that is the part that matters for a
	 * live stream. A single logical step now arrives as two renders: the
	 * mutation's own response, then the pushed frame for the same tick. The
	 * second render sees the car already at its new tile, so a freshly derived
	 * duration would be 0 — and setting `transition-duration: 0` on a running
	 * transition cancels it and snaps the car to the end, which is a visible
	 * teleport exactly like the bug this animation exists to prevent. Reusing
	 * the stored duration for an unchanged position leaves the transition alone.
	 */
	const lastMove = new Map<string, { x: number; y: number; ms: number }>();

	/**
	 * Longest a single catch-up glide may last. A hidden tab or a burst tick can
	 * move a car many tiles at once; without a cap the car would crawl across
	 * the map long after the state settled.
	 */
	const MAX_GLIDE_MS = 600;

	/**
	 * The cars to draw, one element per car keyed by id.
	 *
	 * They used to be glyphs painted inside the tile buttons, which meant a car
	 * could only ever sit on a tile boundary: every step was a discrete jump of
	 * a whole tile, and at 4x a poll advanced the clock six ticks at once so the
	 * car teleported six tiles. As their own absolutely-positioned elements they
	 * keep their identity across a move and can transition left/top, so a step
	 * reads as driving rather than as a redraw.
	 *
	 * Each entry carries the duration for *its own* move: a car that advanced
	 * three tiles gets three tiles' worth of glide (capped), because a single
	 * fixed duration would race across two tiles and crawl across half of one.
	 *
	 * Only the first car on a tile is painted: rows can carry one digit, and a
	 * depot holding four cars would otherwise stack four glyphs on one square.
	 * The others are still rendered (invisible) so that promoting one of them
	 * when the car in front drives off animates, instead of appearing in place.
	 */
	/**
	 * How long a car's current move should take, given where it was last
	 * painted and where it is now.
	 *
	 * Shared by `drawn` (which needs it for the style attribute) and the
	 * bookkeeping effect (which stores it), so the two can never disagree.
	 */
	function glideFor(
		prev: { x: number; y: number; ms: number } | undefined,
		c: CarView
	): number {
		if (!prev) return 0; // first sight of this car: it appeared, it did not move
		const tiles = Math.max(Math.abs(c.x - prev.x), Math.abs(c.y - prev.y));
		// Already at the tracked tile: reuse the duration of the move that put
		// it there, so a repeat frame for the same tick does not cancel the
		// transition mid-flight (see lastMove).
		if (tiles === 0) return prev.ms;
		if (moveMs === 0) return 0;
		return Math.min(moveMs * tiles, MAX_GLIDE_MS);
	}

	const drawn = $derived.by(() => {
		const out: {
			car: CarView;
			digit: number;
			first: boolean;
			here: CarView[];
			glideMs: number;
		}[] = [];
		const seen = new Set<string>();
		for (let i = 0; i < cars.length; i++) {
			const c = cars[i];
			const row = rows[c.y];
			// A car on an unexplored tile is not drawn at all: rows render '?'
			// there, and an overlay that leaked the position would make the fog
			// decorative rather than a gate.
			if (row === undefined || c.x < 0 || c.x >= row.length || row[c.x] === '?') continue;
			const k = `${c.x},${c.y}`;
			const first = !seen.has(k);
			seen.add(k);

			// A car seen for the first time is not moving: it just appeared, so
			// it must not glide in from wherever a same-numbered car used to be.
			const glideMs = glideFor(lastMove.get(c.id), c);

			out.push({ car: c, digit: i + 1, first, here: carsAt.get(k) ?? [c], glideMs });
		}
		return out;
	});

	// Remember what was just painted, for the next snapshot to compare against.
	// Built into a fresh map rather than clearing in place, because the stored
	// durations are derived from the previous entries.
	$effect(() => {
		const next = new Map<string, { x: number; y: number; ms: number }>();
		for (const c of cars) next.set(c.id, { x: c.x, y: c.y, ms: glideFor(lastMove.get(c.id), c) });
		lastMove.clear();
		for (const [k, v] of next) lastMove.set(k, v);
	});

	/**
	 * Coordinates of the order's two endpoints, so the player can see where the
	 * highlighted job actually goes rather than hunting for K and H markers.
	 */
	const highlightTiles = $derived.by(() => {
		const set = new Set<string>();
		if (highlightKitchen) {
			const k = city.kitchens.find((x) => x.id === highlightKitchen);
			if (k) set.add(`${k.x},${k.y}`);
		}
		if (highlightCustomer) {
			const c = city.customers.find((x) => x.id === highlightCustomer);
			if (c) set.add(`${c.x},${c.y}`);
		}
		return set;
	});

	type SiteState = { x: number; y: number; state: 'pending' | 'assigned' | 'carrying'; hasPending: boolean; fleetNumbers: number[] };

	// An endpoint can have several active orders; cargo in transit takes
	// precedence over an assignment still awaiting pickup.
	const siteStates = $derived.by(() => {
		const kitchens = new Map(city.kitchens.map((site) => [site.id, site]));
		const customers = new Map(city.customers.map((site) => [site.id, site]));
		const fleetNumbers = new Map(cars.map((car, index) => [car.id, index + 1]));
		const states = new Map<string, SiteState>();
		function mark(site: { x: number; y: number } | undefined, glyph: string, state: SiteState['state'], number: number | undefined) {
			if (!site) return;
			const ch = rows[site.y]?.[site.x];
			// Never draw an endpoint through fog or unrelated terrain. A car digit
			// can legitimately cover the site's own glyph.
			if (!ch || (ch !== glyph && carIndex(ch) === null)) return;
			const key = `${site.x},${site.y}`;
			let entry = states.get(key);
			if (!entry) {
				entry = { x: site.x, y: site.y, state, hasPending: state === 'pending', fleetNumbers: [] };
				states.set(key, entry);
			} else {
				if (state === 'pending') entry.hasPending = true;
				else if (state === 'carrying' || entry.state === 'pending') entry.state = state;
			}
			if (number !== undefined && !entry.fleetNumbers.includes(number)) entry.fleetNumbers.push(number);
		}
		for (const order of orders) {
			if (order.state === 'PENDING') {
				mark(customers.get(order.customerId), 'H', 'pending', undefined);
				continue;
			}
			const state = order.state === 'PICKED_UP' ? 'carrying' : order.state === 'ASSIGNED' ? 'assigned' : null;
			if (!state) continue;
			const number = order.assignedCar ? fleetNumbers.get(order.assignedCar) : undefined;
			mark(kitchens.get(order.kitchenId), 'K', state, state === 'assigned' ? number : undefined);
			mark(customers.get(order.customerId), 'H', state, number);
		}
		for (const site of states.values()) site.fleetNumbers.sort((a, b) => a - b);
		return states;
	});

	function siteStatusLabel(site: SiteState | undefined): string {
		if (!site) return '';
		const status = site.state === 'carrying' ? 'order picked up' : site.state === 'assigned' ? 'assigned, awaiting pickup' : 'unassigned order';
		const pending = site.hasPending && site.state !== 'pending' ? ', unassigned order' : '';
		return `${status}${pending}${site.fleetNumbers.length ? `, fleet ${site.fleetNumbers.join(', ')}` : ''}`;
	}

	function handleTile(x: number, y: number) {
		const here = carsAt.get(`${x},${y}`);
		if (!here || here.length === 0 || !onSelectCar) return;
		// Several cars on one depot tile: clicking cycles through them, starting
		// from the one after the current selection. Picking the first one always
		// would make the cars behind it reachable only by clicking blind.
		const i = here.findIndex((c) => c.id === selectedCarId);
		onSelectCar(here[(i + 1) % here.length].id);
	}

	// Track the viewport so TILE can adapt. A fixed tile size clipped the
	// controls below the map on shorter screens.
	$effect(() => {
		if (typeof window === 'undefined') return;
		const sync = () => {
			viewportH = window.innerHeight;
			viewportW = window.innerWidth;
		};
		sync();
		window.addEventListener('resize', sync);
		return () => window.removeEventListener('resize', sync);
	});
</script>

<div class="rounded-lg border border-edge-strong bg-[var(--color-map)] p-3">
	<div
		class="relative mx-auto"
		style="width: {city.gridSize * TILE}px; height: {city.gridSize * TILE}px;"
	>
		{#each rows as row, y (y)}
			{#each [...row] as ch, x (x)}
				{@const ci = carIndex(ch)}
				{@const style = ci === null ? glyphStyle(ch) : carGlyphStyle()}
				{@const here = carsAt.get(`${x},${y}`) ?? []}
				{@const highlighted = highlightTiles.has(`${x},${y}`)}
				{@const site = siteStates.get(`${x},${y}`)}
				{@const siteState = site?.state}
				{@const flush = ci !== null || ch === '.' || ch === ',' || ch === 'X' || ch === 'D' || ch === 'C' || ch === 'K' || ch === 'H' || ch === 'h' || ch === 'B' || ch === '#' || ch === '?'}
				<button
					type="button"
					class="absolute flex items-center justify-center text-[12px] leading-none transition-[filter] hover:brightness-150 {flush
						? 'rounded-none'
						: 'rounded-[3px]'} {style.cls} {highlighted ? 'ring-2 ring-[var(--color-body)] z-10' : ''} {ch === 'B' ? 'roof' : ''} {ch === '#' ? 'water' : ''} {ch === ',' ? 'ground' : ''} {ch === '?' ? 'fog' : ''} {ch === 'X' ? 'works' : ''} {ch === '.' ? roadDirection(x, y) : ''}"
					style="left: {x * TILE}px; top: {y * TILE}px; width: {flush ? TILE : TILE - 1}px; height: {flush
						? TILE
						: TILE - 1}px; background-position: {-x * TILE}px {-y * TILE}px; {ch === 'B' ? `border-top: ${rows[y - 1]?.[x] === 'B' ? 0 : 1}px solid var(--color-roof-edge); border-right: ${row[x + 1] === 'B' ? 0 : 1}px solid var(--color-roof-edge); border-bottom: ${rows[y + 1]?.[x] === 'B' ? 0 : 1}px solid var(--color-roof-edge); border-left: ${row[x - 1] === 'B' ? 0 : 1}px solid var(--color-roof-edge);` : ''}"
					title="{style.label}{site ? ` — ${siteStatusLabel(site)}` : ''} [{x},{y}]"
					aria-label="{style.label} at {x},{y}{site ? `, ${siteStatusLabel(site)}` : ''}"
					disabled={here.length === 0}
					tabindex={-1}
					onclick={() => handleTile(x, y)}
				>
					{#if ch === 'K' || ch === 'H' || ch === 'D' || ch === 'C' || ch === 'h'}
						<span class="h-full w-full p-px {ch === 'h' ? 'text-[var(--color-customer)]' : 'text-white'} {siteState === 'carrying' ? 'drop-shadow-[0_0_4px_#f59e0b]' : siteState === 'assigned' ? 'drop-shadow-[0_0_4px_#0284c7]' : siteState === 'pending' ? 'drop-shadow-[0_0_4px_#dc2626]' : ''}"><MapIcon kind={ch} /></span>
					{/if}
				</button>
			{/each}
		{/each}

		<!-- The car layer. Sits above the terrain and below nothing, so a car
		     parked on a kitchen still reads as a car. -->
		<div class="pointer-events-none absolute inset-0">
			{#each drawn as d (d.car.id)}
				{@const car = d.car}
				{@const selected = d.here.some((c) => c.id === selectedCarId)}
				{@const carrying = d.here.some((c) => c.cargo.length > 0)}
				{@const assigned = !carrying && d.here.some((c) => assignedCarIds.has(c.id))}
				<!-- Hidden cars retain their elements so promotion still animates. -->
				<button
					type="button"
					class="absolute flex items-center justify-center rounded-[3px] bg-ink text-[12px] leading-none font-bold text-white {d.first
						? 'pointer-events-auto'
						: 'invisible'} {selected
						? 'z-20 ring-2 ring-[var(--color-charger)] ring-offset-1 ring-offset-[var(--color-map)]'
						: 'z-10'} {carrying ? 'drop-shadow-[0_0_4px_#f59e0b]' : assigned ? 'drop-shadow-[0_0_4px_#0284c7]' : ''}"
					style="left: {car.x * TILE}px; top: {car.y * TILE}px; width: {TILE}px; height: {TILE}px; transition: left {d.glideMs}ms linear, top {d.glideMs}ms linear;"
					title="{d.here.map((c) => `${c.name} (${c.state}${c.cargo.length ? ', carrying an order' : assignedCarIds.has(c.id) ? ', assigned to an order' : ''})`).join(', ')}{d.here.length > 1
						? ` — ${d.here.length} cars here; click to cycle`
						: ''} [{car.x},{car.y}]"
					aria-label="{d.here.length} car{d.here.length === 1
						? ''
						: 's'} at {car.x},{car.y}, {d.here
						.map((c) => `${c.name}${c.cargo.length ? ' (carrying an order)' : assignedCarIds.has(c.id) ? ' (assigned to an order)' : ''}`)
						.join(', ')}{siteStates.has(`${car.x},${car.y}`) ? `; location: ${siteStatusLabel(siteStates.get(`${car.x},${car.y}`))}` : ''}"
					tabindex={d.first ? 0 : -1}
					onclick={() => handleTile(car.x, car.y)}
				>
					<span class="h-full w-full p-px" class:text-[var(--color-danger)]={car.state === 'STRANDED'}><MapIcon kind={car.className.toLowerCase()} /></span>
					{#if carrying || assigned}
						<span class="absolute -top-0.5 -left-0.5 h-1.5 w-1.5 rounded-full ring-1 {carrying ? 'bg-amber-300 ring-amber-900' : 'bg-sky-300 ring-sky-900'}" aria-hidden="true"></span>
					{/if}
					<span class="absolute -right-1 -bottom-1 rounded-full bg-ink px-0.5 text-[9px] leading-none text-white">{d.digit}</span>
					{#if d.first && d.here.length > 1}
						<span
							class="absolute -top-0.5 -right-0.5 rounded-full bg-ink px-1 text-[11px] font-bold text-white"
							>×{d.here.length}</span
						>
					{/if}
				</button>
			{/each}
		</div>
		<!-- Keep endpoint status visible even when a vehicle covers its tile. -->
		<div class="pointer-events-none absolute inset-0 z-30" aria-hidden="true">
			{#each [...siteStates.values()] as site (`${site.x},${site.y}`)}
				<span class="absolute h-1.5 w-1.5 rounded-full ring-1 {site.state === 'carrying' ? 'bg-amber-300 ring-amber-900 drop-shadow-[0_0_4px_#f59e0b]' : site.state === 'assigned' ? 'bg-sky-300 ring-sky-900 drop-shadow-[0_0_4px_#0284c7]' : 'bg-red-500 ring-red-900 drop-shadow-[0_0_4px_#dc2626]'}" style="left: {(site.x + 1) * TILE - 4}px; top: {site.y * TILE - 2}px;"></span>
				{#if site.hasPending && site.state !== 'pending'}
					<span class="absolute h-1.5 w-1.5 rounded-full bg-red-500 ring-1 ring-red-900 drop-shadow-[0_0_4px_#dc2626]" style="left: {site.x * TILE - 2}px; top: {site.y * TILE - 2}px;"></span>
				{/if}
				{#if site.fleetNumbers.length}
					<span class="absolute whitespace-nowrap rounded-sm bg-ink px-0.5 text-[9px] leading-none font-bold tabular-nums text-white" style="left: {(site.x + 1) * TILE - 2}px; top: {(site.y + 1) * TILE - 5}px; transform: translateX(-50%);">{site.fleetNumbers.join(',')}</span>
				{/if}
			{/each}
		</div>
	</div>

	<div class="mt-3 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-muted">
		<span class="flex items-center gap-1">
			<span class="inline-block h-3 w-3 rounded-[2px] bg-[var(--color-road)]" aria-hidden="true"></span>road
		</span>
		<span class="flex items-center gap-1">
			<span class="inline-block h-3 w-3 rounded-[2px] bg-[var(--color-ground)]" aria-hidden="true"></span>ground&nbsp;(2⚡)
		</span>
		<span class="flex items-center gap-1">
			<span class="inline-block h-3 w-3 rounded-[2px] bg-[var(--color-water)]" aria-hidden="true"></span>water
		</span>
		{#each [{ kind: 'D', label: 'depot', color: 'bg-[var(--color-depot)] text-white' }, { kind: 'C', label: 'charger', color: 'bg-[var(--color-charger)] text-white' }, { kind: 'K', label: 'kitchen', color: 'bg-[var(--color-kitchen)] text-white' }, { kind: 'H', label: 'customer waiting', color: 'bg-[var(--color-customer)] text-white' }, { kind: 'h', label: 'house, nobody home', color: 'bg-[var(--color-customer)]/20 text-[var(--color-customer)]' }] as item (item.kind)}
			<span class="flex items-center gap-1">
				<span class="inline-block h-4 w-4 rounded-[2px] p-px {item.color}" aria-hidden="true"><MapIcon kind={item.kind} /></span>{item.label}
			</span>
		{/each}
		<span class="flex items-center gap-1">
			<span class="inline-block h-3 w-3 rounded-[2px] bg-[var(--color-building)]" aria-hidden="true"></span>building
		</span>
		<span class="flex items-center gap-1">
			<span class="fog inline-block h-3 w-3" aria-hidden="true"></span>unexplored
		</span>
		<span class="flex items-center gap-1">
			<span
				class="inline-flex h-4 w-4 items-center justify-center rounded-[2px] bg-ink p-px text-white"
				aria-hidden="true"><MapIcon kind="van" /></span
			>car
		</span>
		<span class="flex items-center gap-1">
			<span class="h-2 w-2 rounded-full bg-red-500 ring-1 ring-red-900" aria-hidden="true"></span>unassigned order
		</span>
		<span class="flex items-center gap-1">
			<span class="h-2 w-2 rounded-full bg-sky-300 ring-1 ring-sky-900" aria-hidden="true"></span>assigned, awaiting pickup
		</span>
		<span class="flex items-center gap-1">
			<span class="h-2 w-2 rounded-full bg-amber-300 ring-1 ring-amber-900" aria-hidden="true"></span>order picked up
		</span>
		{#if closures.length > 0}
			<span class="flex items-center gap-1 text-[var(--color-warn)]">
				<span class="inline-flex h-3.5 w-3.5 items-center justify-center rounded-[2px] bg-[var(--color-warn)]/40 text-[11px] font-bold text-ink" aria-hidden="true"
					>X</span
				>works
			</span>
		{/if}
		<span class="ml-auto">{city.hiddenTiles} tiles still dark</span>
	</div>

	{#if closures.length > 0}
		<ul class="mt-2 space-y-1 border-t border-edge pt-2 text-xs">
			{#each closures as c (c.id)}
				<li class="flex items-center justify-between gap-2 text-muted">
					<span class="text-[var(--color-warn)]">{c.reason}</span>
					<span class="text-faint">
						at ({c.minX},{c.minY}) · reopens in {Math.max(0, c.ticksLeft)} tick{Math.max(0, c.ticksLeft) === 1
							? ''
							: 's'}
					</span>
				</li>
			{/each}
		</ul>
	{/if}
</div>

<style>
	.roof {
		background-image: repeating-linear-gradient(135deg, transparent 0 7px, rgb(255 255 255 / 0.1) 7px 8px);
	}
	.water {
		background-image: repeating-linear-gradient(170deg, transparent 0 12px, rgb(255 255 255 / 0.22) 12px 13px, transparent 13px 20px);
	}
	.ground {
		background-image: radial-gradient(rgb(142 118 78 / 0.2) 0.7px, transparent 1px);
		background-size: 8px 8px;
	}
	.fog {
		background-color: #d7dfea;
		background-image: repeating-linear-gradient(135deg, transparent 0 14px, rgb(126 144 168 / 0.1) 14px 15px, transparent 15px 28px);
	}
	.works {
		background-image: repeating-linear-gradient(135deg, transparent 0 3px, rgb(146 64 14 / 0.45) 3px 5px);
	}
	.road-vertical::after, .road-horizontal::after {
		content: '';
		position: absolute;
		background: var(--color-road-marking);
		border-radius: 1px;
	}
	.road-vertical::after { width: 1px; height: 40%; }
	.road-horizontal::after { width: 40%; height: 1px; }
</style>
