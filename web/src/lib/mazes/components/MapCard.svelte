<script lang="ts">
	import type { GameMap } from '$lib/mazes/types';
	import { isDraft } from '$lib/mazes/maps';
	import MiniGrid from './MiniGrid.svelte';

	let {
		map,
		busy = false,
		onplay,
		editHref = null,
		canEdit = true,
		canDuplicate = true
	}: {
		map: GameMap;
		busy?: boolean;
		onplay?: (map: GameMap) => void;
		editHref?: string | null;
		/** The viewer owns the map, or is acting as an admin. */
		canEdit?: boolean;
		/** Duplicating needs the full layout: your own map, or an admin key for someone else's. */
		canDuplicate?: boolean;
	} = $props();

	const badge: Record<string, string> = {
		easy: 'bg-emerald-100 text-emerald-800',
		medium: 'bg-amber-100 text-amber-800',
		hard: 'bg-rose-100 text-rose-800'
	};
	const levels = $derived(`${map.levelCount} ${map.levelCount === 1 ? 'level' : 'levels'}`);
</script>

<article class="db-panel flex h-full flex-col gap-3 p-4" data-testid="map-card">
	<div class="flex h-48 items-center justify-center rounded-lg bg-indigo-50/60" data-testid="map-thumb">
		<div class="flex aspect-square h-full items-center justify-center p-4">
			<MiniGrid fit rows={map.preview} levelCount={map.levelCount} label="Preview of {map.name}, {levels}" />
		</div>
	</div>
	<div>
		<h3 class="font-semibold text-indigo-950">{map.name}</h3>
		<p class="mt-0.5 text-sm text-slate-600 line-clamp-2" title={map.description}>{map.description}</p>
	</div>
	<div class="flex flex-wrap gap-1.5 text-xs">
		{#if map.ownedByMe}
			<span class="rounded-full bg-violet-100 px-2 py-0.5 font-medium text-violet-900" data-testid="map-yours">Your map</span>
		{/if}
		{#if isDraft(map)}
			<span class="rounded-full bg-amber-100 px-2 py-0.5 font-medium text-amber-900" data-testid="map-draft">Draft</span>
		{:else if map.ownedByMe}
			<span class="rounded-full bg-emerald-50 px-2 py-0.5 text-emerald-800" data-testid="map-published">Published</span>
		{/if}
		<span class="rounded-full px-2 py-0.5 {badge[map.difficulty] ?? 'bg-slate-100 text-slate-700'}">{map.difficulty}</span>
		<span class="rounded-full bg-indigo-50 text-indigo-800 px-2 py-0.5">{map.width}×{map.height} · {levels}</span>
		{#if map.parMoves !== null}
			<span class="rounded-full bg-indigo-50 text-indigo-800 px-2 py-0.5">par {map.parMoves}</span>
		{/if}
		<span class="rounded-full bg-indigo-50 text-indigo-800 px-2 py-0.5">≤ {map.maxMoves} moves</span>
	</div>
	{#if isDraft(map)}
		<p class="text-xs text-amber-800">Only you can see this draft. Play it, then publish it when the finish is reachable.</p>
	{/if}
	<div class="mt-auto space-y-2">
		{#if onplay}
			<button
				type="button"
				class="db-button w-full"
				disabled={busy}
				onclick={() => onplay?.(map)}>Play</button
			>
		{/if}
		{#if editHref && (canEdit || canDuplicate)}
			<div class="flex gap-2">
				{#if canEdit}
					<a href={editHref} class="flex-1 rounded-lg border border-indigo-300 px-3 py-1.5 text-center text-sm text-indigo-800 hover:bg-indigo-50">Edit</a>
				{/if}
				{#if canDuplicate}
					<a href="{editHref}&duplicate=1" class="flex-1 rounded-lg border border-indigo-300 px-3 py-1.5 text-center text-sm text-indigo-800 hover:bg-indigo-50">Duplicate</a>
				{/if}
			</div>
		{/if}
	</div>
</article>
