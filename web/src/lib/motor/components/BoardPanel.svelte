<script lang="ts">
	import type { Snippet } from 'svelte';
	import type { GameMap, StepEvent, VMState } from '$lib/motor/types';
	import Grid from './Grid.svelte';
	import TileLegend from './TileLegend.svelte';

	/**
	 * The board column shared by /play, /watch and /map, so a map looks the same before and
	 * after Play and the three pages cannot drift apart.
	 *
	 * pinned: the play-page treatment — on phones the board sticks to the top while the controls
	 * and the program scroll under it, and the legend/description move below the program. Pages
	 * that repeat them further down pass pinned and re-render the description/legend there.
	 * vm null draws the map's start state (a map nobody has played yet).
	 */
	let {
		map,
		vm = null,
		lastEvent = null,
		pinned = false,
		legend = true,
		description = true,
		children
	}: {
		map: GameMap;
		vm?: VMState | null;
		lastEvent?: StepEvent | null;
		pinned?: boolean;
		legend?: boolean;
		description?: boolean;
		children?: Snippet;
	} = $props();

	// Only a very tall map is capped (50vh) on phones so the program panel keeps room; it always fills the column width.
	const pinnedClasses =
		'min-w-0 space-y-3 max-lg:sticky max-lg:top-0 max-lg:z-30 max-lg:-mx-4 max-lg:border-b max-lg:border-indigo-200 max-lg:px-4 max-lg:py-1 max-lg:bg-[var(--page-bg)] sm:max-lg:-mx-6 sm:max-lg:px-6 lg:sticky lg:top-4 lg:self-start';
	const plainClasses = 'space-y-3 lg:sticky lg:top-4 lg:self-start';
</script>

<section aria-label="Board" class={pinned ? pinnedClasses : plainClasses}>
	<div class="mx-auto max-lg:max-w-[min(100%,calc(50vh*var(--ar)))]" style="--ar: {map.width / map.height}">
		<Grid {map} {vm} {lastEvent} />
	</div>
	{#if description}
		<p class="text-sm text-slate-600 {pinned ? 'max-lg:hidden' : ''}">{map.description}</p>
	{/if}
	{#if legend}
		<div class={pinned ? 'max-lg:hidden' : ''}><TileLegend {map} /></div>
	{/if}
	{@render children?.()}
</section>
