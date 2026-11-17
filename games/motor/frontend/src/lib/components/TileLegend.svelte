<script lang="ts">
	import { TILE_NOTE, tileKindsOnMap } from '$lib/tileLegend';
	import type { GameMap } from '$lib/types';

	let { map }: { map: GameMap } = $props();

	const kinds = $derived(tileKindsOnMap(map));
</script>

<!-- Only on maps that use tiles; the full rules are on the How it works page. -->
{#if kinds.length > 0}
	<details open class="rounded-lg border border-indigo-200 bg-white px-3 py-2 text-sm" data-testid="tile-legend">
		<summary class="cursor-pointer font-semibold text-indigo-950">Tiles on this map</summary>
		<dl class="mt-2 grid grid-cols-[max-content_1fr] gap-x-3 gap-y-1.5">
			{#each kinds as k (k.id)}
				<dt class="font-mono text-xs font-semibold text-indigo-900"><code>{k.glyph}</code></dt>
				<dd><span class="font-semibold text-indigo-950">{k.name}.</span> <span class="text-slate-700">{k.text}</span></dd>
			{/each}
		</dl>
		<p class="mt-2 text-xs text-slate-600">{TILE_NOTE} <a href="/learn#tiles" class="text-indigo-700 underline">All the rules</a></p>
	</details>
{/if}
