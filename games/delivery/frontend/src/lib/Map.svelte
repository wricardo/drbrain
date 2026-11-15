<script lang="ts">
	import type { CarView, CityView } from '$lib/api';
	import { batteryTone, carIndex, glyphStyle } from '$lib/glyphs';

	interface Props {
		city: CityView;
		cars: CarView[];
		selectedCarId: string | null;
		/** Kitchen and customer of the order being highlighted, if any. */
		highlightKitchen?: string | null;
		highlightCustomer?: string | null;
		onSelectCar?: (id: string) => void;
	}

	let {
		city,
		cars,
		selectedCarId,
		highlightKitchen = null,
		highlightCustomer = null,
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

	/** Cars keyed by the glyph digit that represents them in the rows. */
	const carAt = $derived.by(() => {
		const map = new Map<string, CarView>();
		for (const c of cars) map.set(`${c.x},${c.y}`, c);
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
		const car = carAt.get(`${x},${y}`);
		if (car && onSelectCar) onSelectCar(car.id);
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
				{@const car = ci === null ? undefined : cars[ci]}
				{@const selected = car && car.id === selectedCarId}
				{@const highlighted = highlightTiles.has(`${x},${y}`)}
				<button
					type="button"
					class="absolute flex items-center justify-center rounded-[3px] text-[12px] leading-none transition-[filter] hover:brightness-150 {car
						? 'bg-white text-ink-950 font-bold'
						: style.cls} {selected ? 'ring-2 ring-[var(--color-charger)] ring-offset-1 ring-offset-ink-900 z-10' : ''} {highlighted ? 'ring-2 ring-white z-10' : ''}"
					style="left: {x * TILE}px; top: {y * TILE}px; width: {TILE - 2}px; height: {TILE - 2}px;"
					title="{style.label}{car ? ` — ${car.name} (${car.state})` : ''} [{x},{y}]"
					aria-label="{car ? `Car ${car.name} at ${x},${y}` : `${style.label} at ${x},${y}`}"
					disabled={!car}
					onclick={() => handleTile(x, y)}
				>
					{#if car}
						<span class:text-[var(--color-danger)]={car.state === 'STRANDED'}>
							{ci! + 1}
						</span>
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
			<span class="inline-block h-3 w-3 rounded-[2px] bg-[var(--color-ground)]"></span>ground (2⚡)
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
			<span class="inline-block h-3 w-3 rounded-[2px] border border-ink-500 bg-ink-950"></span
			>unexplored
		</span>
		<span class="flex items-center gap-1">
			<span
				class="inline-flex h-3.5 w-3.5 items-center justify-center rounded-[2px] bg-white text-[9px] font-bold text-ink-950"
				>1</span
			>car
		</span>
		<span class="ml-auto">{city.hiddenTiles} tiles still dark</span>
	</div>
</div>

<style>
	/* The battery tone helper lives in glyphs.ts; the map only needs the one
	   colour that changes meaning (stranded), handled inline above. */
</style>
