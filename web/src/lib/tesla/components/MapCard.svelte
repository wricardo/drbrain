<script lang="ts">
	import MapThumb from './MapThumb.svelte';
	import { countTiles, isDraft, prettyMapName, type MapLayout, type MapSummary } from '$lib/tesla/maps';

	let {
		map,
		preview = undefined,
		previewError = false,
		busy = false,
		onplay
	}: {
		map: MapSummary;
		preview?: MapLayout;
		previewError?: boolean;
		busy?: boolean;
		onplay: (map: MapSummary) => void;
	} = $props();

	const name = $derived(prettyMapName(map.name || map.mapId));
	const parks = $derived(preview ? countTiles(preview, 'park') : null);
</script>

<article class="db-panel flex h-full flex-col gap-3 p-4" data-testid="map-card">
	{#if preview}
		<MapThumb map={preview} label={`Preview of ${name}`} />
	{:else if previewError}
		<div class="aspect-square w-full rounded-xl bg-red-50 flex items-center justify-center text-xs text-red-700 p-4 text-center">Could not load preview</div>
	{:else}
		<div class="aspect-square w-full rounded-xl bg-[#f7f7f7] animate-pulse"></div>
	{/if}
	<div class="min-w-0">
		<h3 class="truncate text-lg font-semibold tracking-tight">
			<a
				href={`/tesla/maps/${encodeURIComponent(map.mapId)}`}
				title={`Open ${name}`}
				class="hover:text-red-600 hover:underline underline-offset-2 transition-colors"
			>{name}</a>
		</h3>
		<p class="text-sm text-gray-500 leading-relaxed line-clamp-2 min-h-[2.5rem]">{map.description}</p>
	</div>
	<div class="flex flex-wrap gap-1.5 text-xs">
		{#if map.ownedByMe}
			<span class="rounded-full bg-[#fbeae5] text-[#962d26] px-2 py-0.5 font-medium" data-testid="map-yours">Your map</span>
		{/if}
		{#if isDraft(map)}
			<span class="rounded-full bg-amber-100 text-amber-900 px-2 py-0.5 font-medium" data-testid="map-draft">Draft</span>
		{:else if map.builtin === false}
			<span class="rounded-full bg-emerald-50 text-emerald-800 px-2 py-0.5" data-testid="map-published">Published</span>
		{/if}
		<span class="rounded-full bg-[#f7f7f7] text-[#393c41] px-2 py-0.5">{map.gridSize}×{map.gridSize}</span>
		<span class="rounded-full bg-emerald-50 text-emerald-800 px-2 py-0.5">🌳 {parks ?? '…'} parks</span>
		<span class="rounded-full bg-[#f7f7f7] text-[#393c41] px-2 py-0.5">🔋 ≤ {map.maxBattery} battery</span>
	</div>
	{#if isDraft(map)}
		<p class="text-xs text-amber-800">Only you can see this draft. Play it as often as you like, then publish it.</p>
	{/if}
	<div class="mt-auto">
		<button
			type="button"
			onclick={() => onplay(map)}
			disabled={busy}
			class="db-button w-full"
		>{busy ? 'Starting…' : 'Play'}</button>
	</div>
</article>
