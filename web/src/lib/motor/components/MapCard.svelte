<script lang="ts">
	import type { GameMap } from '$lib/motor/types';
	import { canEditMap, splitMapName } from '$lib/motor/maps';
	import { recursionChip } from '$lib/motor/recursion';
	import MiniGrid from './MiniGrid.svelte';

	let {
		map,
		busy = false,
		onplay,
		editHref = null
	}: { map: GameMap; busy?: boolean; onplay?: (map: GameMap) => void; editHref?: string | null } = $props();

	const parts = $derived(splitMapName(map.name));
	const recursion = $derived(recursionChip(map));
	const badge: Record<string, string> = {
		easy: 'bg-emerald-100 text-emerald-800',
		medium: 'bg-amber-100 text-amber-800',
		hard: 'bg-rose-100 text-rose-800'
	};
</script>

<article class="db-panel flex h-full flex-col gap-3 p-4" data-testid="map-card">
	<div class="flex aspect-square w-full items-center justify-center rounded-lg bg-indigo-50/60 p-4" data-testid="map-thumb">
		<MiniGrid layout={map.layout} label="Preview of {map.name}" fit />
	</div>
	<div>
		{#if parts.series}
			<p class="text-xs font-bold uppercase tracking-wider text-indigo-600" data-testid="map-series">{parts.series}</p>
		{/if}
		<h3 class="font-semibold text-indigo-950" title={map.name}>
			<a href="/motor/map/{encodeURIComponent(map.id)}" class="hover:text-indigo-700 hover:underline" data-testid="map-name-link">{parts.title}</a>
		</h3>
		<p class="mt-0.5 min-h-[2.5rem] text-sm text-slate-600 line-clamp-2" title={map.description}>{map.description}</p>
	</div>
	<div class="flex flex-wrap gap-1.5 text-xs">
		<span class="rounded-full px-2 py-0.5 {badge[map.difficulty] ?? 'bg-slate-100 text-slate-700'}">{map.difficulty}</span>
		{#if !map.builtin}
			<span class="rounded-full bg-violet-100 text-violet-800 px-2 py-0.5" data-testid="player-map">player-made</span>
		{/if}
		{#if map.status === 'DRAFT'}
			<span class="rounded-full bg-amber-100 text-amber-900 px-2 py-0.5" data-testid="map-draft">draft</span>
		{/if}
		<span class="rounded-full bg-indigo-50 text-indigo-800 px-2 py-0.5">{map.width}×{map.height}</span>
		<span class="rounded-full bg-indigo-50 text-indigo-800 px-2 py-0.5">{map.treats.length} {map.treats.length === 1 ? 'treat' : 'treats'}</span>
		<span class="rounded-full bg-indigo-50 text-indigo-800 px-2 py-0.5">≤ {map.maxSteps} steps</span>
		<span class="rounded-full px-2 py-0.5 {recursion.classes}" title={recursion.title} data-testid="map-recursion"
			>{recursion.label}</span
		>
	</div>
	<p class="text-xs text-slate-600">Main {map.mainTapeLength} slots{map.subTapeLengths.length ? ` · subs ${map.subTapeLengths.join(' / ')}` : ''}</p>
	<div class="mt-auto space-y-2">
		{#if onplay}
			<button
				type="button"
				class="db-button w-full"
				disabled={busy}
				onclick={() => onplay?.(map)}>Play</button
			>
		{/if}
		{#if editHref}
			<div class="flex gap-2">
				{#if canEditMap(map)}
					<a href={editHref} class="flex-1 rounded-lg border border-indigo-300 px-3 py-1.5 text-center text-sm text-indigo-800 hover:bg-indigo-50">Edit</a>
				{/if}
				<a href="{editHref}&duplicate=1" class="flex-1 rounded-lg border border-indigo-300 px-3 py-1.5 text-center text-sm text-indigo-800 hover:bg-indigo-50">Duplicate</a>
			</div>
		{/if}
	</div>
</article>
