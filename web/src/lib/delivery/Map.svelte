<script lang="ts">
	import type { CarView, CityView, ClosureView } from '$lib/delivery/api';
	import { carGlyphStyle, carIndex, glyphStyle } from '$lib/delivery/glyphs';

	interface Props {
		city: CityView;
		cars: CarView[];
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
		cars,
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

	/**
	 * Cars grouped by tile.
	 *
	 * A tile is not limited to one car any more: a depot holds as many as it
	 * authors (4 on the shipped map), and a charger holds its own capacity.
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
				<!--
					Roads are drawn flush so a street reads as a continuous strip.
					A uniform 1-2px gap on every tile made the grid look beaded: a
					row of separate squares rather than a road you drive along.
					Buildings, water and fog keep the gap, which is what makes the
					blocks read as solid masses.
				-->
				{@const flush = ch === '.' || ch === ',' || ch === 'X' || ch === 'D' || ch === 'C' || ch === 'K' || ch === 'H' || ch === 'h'}
				<button
					type="button"
					class="absolute flex items-center justify-center text-[12px] leading-none transition-[filter] hover:brightness-150 {flush
						? 'rounded-none'
						: 'rounded-[3px]'} {style.cls} {highlighted ? 'ring-2 ring-[var(--color-body)] z-10' : ''}"
					style="left: {x * TILE}px; top: {y * TILE}px; width: {flush ? TILE : TILE - 1}px; height: {flush
						? TILE
						: TILE - 1}px;"
					title="{style.label} [{x},{y}]"
					aria-label="{style.label} at {x},{y}"
					disabled={here.length === 0}
					tabindex={-1}
					onclick={() => handleTile(x, y)}
				>
					{#if ch === 'K' || ch === 'H' || ch === 'D' || ch === 'C' || ch === 'h'}
						<span class="font-semibold {ch === 'h' ? 'text-[var(--color-customer)]' : 'text-white'}">{ch}</span>
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
				<!--
					A car is drawn as a white tile with its fleet number. The
					transition is what makes the map animate: with no duration the
					car jumps a whole tile, and at 4x it jumped five at once.

					A car behind another on the same tile is hidden rather than
					stacked, but stays in the DOM so that when it becomes the tile's
					representative it glides in from wherever it was.
				-->
				<button
					type="button"
					class="absolute flex items-center justify-center rounded-none bg-ink text-[12px] leading-none font-bold text-white {d.first
						? 'pointer-events-auto'
						: 'invisible'} {selected
						? 'z-20 ring-2 ring-[var(--color-charger)] ring-offset-1 ring-offset-[var(--color-map)]'
						: 'z-10'}"
					style="left: {car.x * TILE}px; top: {car.y * TILE}px; width: {TILE}px; height: {TILE}px; transition: left {d.glideMs}ms linear, top {d.glideMs}ms linear;"
					title="{car.name} ({car.state}){d.here.length > 1
						? ` — ${d.here.length} cars here; click to cycle`
						: ''} [{car.x},{car.y}]"
					aria-label="{d.here.length} car{d.here.length === 1
						? ''
						: 's'} at {car.x},{car.y}, {d.here
						.map((c) => c.name)
						.join(', ')}"
					tabindex={d.first ? 0 : -1}
					onclick={() => handleTile(car.x, car.y)}
				>
					<span class:text-[var(--color-danger)]={car.state === 'STRANDED'}>{d.digit}</span>
					{#if d.first && d.here.length > 1}
						<span
							class="absolute -top-0.5 -right-0.5 rounded-full bg-ink px-1 text-[11px] font-bold text-white"
							>×{d.here.length}</span
						>
					{/if}
				</button>
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
		<span class="flex items-center gap-1">
			<span
				class="inline-flex h-3.5 w-3.5 items-center justify-center rounded-[2px] bg-[var(--color-depot)] text-[11px] font-bold text-white"
				aria-hidden="true">D</span
			>depot
		</span>
		<span class="flex items-center gap-1">
			<span
				class="inline-flex h-3.5 w-3.5 items-center justify-center rounded-[2px] bg-[var(--color-charger)] text-[11px] font-bold text-white"
				aria-hidden="true">C</span
			>charger
		</span>
		<span class="flex items-center gap-1">
			<span
				class="inline-flex h-3.5 w-3.5 items-center justify-center rounded-[2px] bg-[var(--color-kitchen)] text-[11px] font-bold text-white"
				aria-hidden="true">K</span
			>kitchen
		</span>
		<span class="flex items-center gap-1">
			<span
				class="inline-flex h-3.5 w-3.5 items-center justify-center rounded-[2px] bg-[var(--color-customer)] text-[11px] font-bold text-white"
				aria-hidden="true">H</span
			>customer waiting
		</span>
		<span class="flex items-center gap-1">
			<span
				class="inline-flex h-3.5 w-3.5 items-center justify-center rounded-[2px] bg-[var(--color-customer)]/20 text-[11px] font-bold text-[var(--color-customer)]"
				aria-hidden="true">h</span
			>house, nobody home
		</span>
		<span class="flex items-center gap-1">
			<span class="inline-block h-3 w-3 rounded-[2px] bg-[var(--color-building)]" aria-hidden="true"></span>building
		</span>
		<span class="flex items-center gap-1">
			<span class="inline-block h-3 w-3 rounded-[2px] border border-dashed border-edge-faint bg-surface-muted" aria-hidden="true"
			></span>unexplored
		</span>
		<span class="flex items-center gap-1">
			<span
				class="inline-flex h-3.5 w-3.5 items-center justify-center rounded-[2px] bg-ink text-[11px] font-bold text-white"
				aria-hidden="true">1</span
			>car
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
