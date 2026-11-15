<script lang="ts">
	import type { CarView, CityView, ClosureView } from '$lib/api';
	import { batteryTone, carIndex, glyphStyle } from '$lib/glyphs';

	interface Props {
		city: CityView;
		cars: CarView[];
		selectedCarId: string | null;
		/** Kitchen and customer of the order being highlighted, if any. */
		highlightKitchen?: string | null;
		highlightCustomer?: string | null;
		/** Street works currently closed. The rows already draw them as 'X'. */
		closures?: ClosureView[];
		onSelectCar?: (id: string) => void;
	}

	let {
		city,
		cars,
		selectedCarId,
		highlightKitchen = null,
		highlightCustomer = null,
		closures = [],
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
		// Budget measured in headless Chrome at 1440x950: header (~44), page
		// padding, the D-pad + keyboard panel (~166), the activity panel (~63)
		// and legend/padding around the grid (~54). 390 leaves a small margin
		// so nothing clips; verified by scripts/ui-smoke.mjs.
		const CHROME_V = 410;
		const CHROME_H = 420;
		const byHeight = Math.floor((viewportH - CHROME_V) / Math.max(city.gridSize, 1));
		const byWidth = Math.floor((viewportW - CHROME_H) / Math.max(city.gridSize, 1));
		return Math.max(14, Math.min(32, byHeight, byWidth));
	});

	/**
	 * Cars grouped by tile.
	 *
	 * A tile is not limited to one car any more: a depot holds as many as it
	 * authors (4 on the shipped map), and a charger holds its own capacity. Rows
	 * can only draw one digit per tile, so a plain `map.set` per car silently made
	 * every car but the last unselectable — the glyph and the click target also
	 * disagreed about which car they meant.
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

	const rows = $derived(city.rows);

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

<div class="overflow-auto rounded-lg border border-ink-600 bg-ink-900 p-3">
	<div
		class="relative mx-auto"
		style="width: {city.gridSize * TILE}px; height: {city.gridSize * TILE}px;"
	>
		{#each rows as row, y (y)}
			{#each [...row] as ch, x (x)}
				{@const style = glyphStyle(ch)}
				{@const ci = carIndex(ch)}
				{@const here = carsAt.get(`${x},${y}`) ?? []}
				<!-- The glyph digit names the car the renderer drew, but on a depot
				     tile several cars share one digit and the last one written wins.
				     Prefer the tile's own list so the label and the click target
				     always describe the same tile. -->
				{@const car = here[0] ?? (ci === null ? undefined : cars[ci])}
				<!-- Rows can only carry one digit, so a depot holding four cars draws
				     one of them. The badge is what stops the other three being
				     invisible: it is the count of cars on this tile. -->
				{@const crowded = here.length > 1}
				{@const selected = car ? here.some((c) => c.id === selectedCarId) : false}
				{@const highlighted = highlightTiles.has(`${x},${y}`)}
				<!--
					Roads are drawn flush so a street reads as a continuous strip.
					A uniform 1-2px gap on every tile made the grid look beaded: a
					row of separate squares rather than a road you drive along.
					Buildings, water and fog keep the gap, which is what makes the
					blocks read as solid masses.
				-->
				{@const flush = ch === '.' || ch === ',' || ch === 'X' || ch === 'D' || ch === 'C' || ch === 'K' || ch === 'H'}
				<button
					type="button"
					class="absolute flex items-center justify-center text-[12px] leading-none transition-[filter] hover:brightness-150 {flush
						? 'rounded-none'
						: 'rounded-[3px]'} {car
						? 'bg-white text-ink-950 font-bold rounded-none'
						: style.cls} {selected ? 'ring-2 ring-[var(--color-charger)] ring-offset-1 ring-offset-ink-900 z-10' : ''} {highlighted ? 'ring-2 ring-white z-10' : ''}"
					style="left: {x * TILE}px; top: {y * TILE}px; width: {flush ? TILE : TILE - 1}px; height: {flush
						? TILE
						: TILE - 1}px;"
					title="{style.label}{car ? ` — ${car.name} (${car.state})` : ''}{crowded
						? ` — ${here.length} cars here; click to cycle`
						: ''} [{x},{y}]"
					aria-label="{car
						? `${here.length} car${here.length === 1 ? '' : 's'} at ${x},${y}, ${here
								.map((c) => c.name)
								.join(', ')}`
						: `${style.label} at ${x},${y}`}"
					disabled={!car}
					onclick={() => handleTile(x, y)}
				>
					{#if car}
						<span class:text-[var(--color-danger)]={car.state === 'STRANDED'}>
							{ci! + 1}
						</span>
						{#if crowded}
							<span
								class="absolute -top-0.5 -right-0.5 rounded-full bg-ink-950 px-1 text-[9px] font-bold text-fog-300"
								>×{here.length}</span
							>
						{/if}
					{:else if ch === 'K' || ch === 'H' || ch === 'D' || ch === 'C'}
						<span class="font-semibold text-ink-950">{ch}</span>
					{/if}
				</button>
			{/each}
		{/each}
	</div>

	<div class="mt-3 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-fog-400">
		<span class="flex items-center gap-1">
			<span class="inline-block h-3 w-3 rounded-[2px] bg-[var(--color-road)]"></span>road
		</span>
		<span class="flex items-center gap-1">
			<span class="inline-block h-3 w-3 rounded-[2px] bg-[var(--color-ground)]"></span>ground&nbsp;(2⚡)
		</span>
		<span class="flex items-center gap-1">
			<span class="inline-block h-3 w-3 rounded-[2px] bg-[var(--color-water)]"></span>water
		</span>
		<span class="flex items-center gap-1">
			<span
				class="inline-flex h-3.5 w-3.5 items-center justify-center rounded-[2px] bg-[var(--color-depot)] text-[9px] font-bold text-ink-950"
				>D</span
			>depot
		</span>
		<span class="flex items-center gap-1">
			<span
				class="inline-flex h-3.5 w-3.5 items-center justify-center rounded-[2px] bg-[var(--color-charger)] text-[9px] font-bold text-ink-950"
				>C</span
			>charger
		</span>
		<span class="flex items-center gap-1">
			<span
				class="inline-flex h-3.5 w-3.5 items-center justify-center rounded-[2px] bg-[var(--color-kitchen)] text-[9px] font-bold text-ink-950"
				>K</span
			>kitchen
		</span>
		<span class="flex items-center gap-1">
			<span
				class="inline-flex h-3.5 w-3.5 items-center justify-center rounded-[2px] bg-[var(--color-customer)] text-[9px] font-bold text-ink-950"
				>H</span
			>customer
		</span>
		<span class="flex items-center gap-1">
			<span class="inline-block h-3 w-3 rounded-[2px] bg-[var(--color-building)]"></span>building
		</span>
		<span class="flex items-center gap-1">
			<span class="inline-block h-3 w-3 rounded-[2px] border border-dashed border-ink-500 bg-ink-950"
			></span>unexplored
		</span>
		<span class="flex items-center gap-1">
			<span
				class="inline-flex h-3.5 w-3.5 items-center justify-center rounded-[2px] bg-white text-[9px] font-bold text-ink-950"
				>1</span
			>car
		</span>
		{#if closures.length > 0}
			<span class="flex items-center gap-1 text-[var(--color-warn)]">
				<span class="inline-flex h-3.5 w-3.5 items-center justify-center rounded-[2px] bg-[var(--color-warn)]/40 text-[9px] font-bold text-ink-950"
					>X</span
				>works
			</span>
		{/if}
		<span class="ml-auto">{city.hiddenTiles} tiles still dark</span>
	</div>

	{#if closures.length > 0}
		<ul class="mt-2 space-y-1 border-t border-ink-800 pt-2 text-xs">
			{#each closures as c (c.id)}
				<li class="flex items-center justify-between gap-2 text-fog-400">
					<span class="text-[var(--color-warn)]">{c.reason}</span>
					<span class="text-fog-500">
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
	/* The battery tone helper lives in glyphs.ts; the map only needs the one
	   colour that changes meaning (stranded), handled inline above. */
</style>
