<script lang="ts">
	import { engraveMeasure, measureWidth, STAVE_BOX_HEIGHT } from '../engrave';
	import type { Measure } from '../types';

	let {
		measure,
		flipH,
		flipV,
		axis,
		keySig,
		timeSig,
		clef,
		position,
		selected,
		onToggle
	}: {
		measure: Measure;
		flipH: boolean;
		flipV: boolean;
		axis: number;
		keySig: string;
		timeSig: string;
		clef: string;
		position: number;
		selected: boolean;
		onToggle: (position: number) => void;
	} = $props();

	let svg = $state('');
	let engravingFailed = $state(false);

	// VexFlow is client-only; the engraver imports it on demand and caches by
	// measure id + flip state. A flip change re-renders just this card.
	$effect(() => {
		let cancelled = false;
		svg = '';
		engravingFailed = false;
		void engraveMeasure(measure, { h: flipH, v: flipV }, { axis, key: keySig, timeSig, clef })
			.then((out) => {
				if (!cancelled) svg = out;
			})
			.catch(() => {
				if (!cancelled) engravingFailed = true;
			});
		return () => {
			cancelled = true;
		};
	});
</script>

<button
	type="button"
	aria-pressed={selected}
	aria-label="Measure {position + 1}{flipH ? ', retrograde' : ''}{flipV ? ', inverted' : ''}"
	data-testid="measure-{position}"
	onclick={() => onToggle(position)}
	style="--stave-w: {measureWidth(measure)}px"
	class="relative w-[var(--stave-w)] max-w-full shrink-0 rounded-md border px-1 pb-1 pt-0.5 text-left transition-colors {selected
		? 'border-amber-700 bg-amber-100 ring-2 ring-amber-600'
		: 'border-amber-300/80 bg-[#fdf8ee] hover:border-amber-500 hover:bg-amber-50'}"
>
	<span class="flex items-center justify-between gap-1 px-1 text-[10px] font-semibold uppercase tracking-wide text-amber-800">
		<span>#{position + 1}</span>
		<span class="flex gap-1">
			{#if flipH}<span title="Retrograde" class="rounded bg-amber-700 px-1 text-white">H</span>{/if}
			{#if flipV}<span title="Inversion" class="rounded bg-stone-700 px-1 text-white">V</span>{/if}
		</span>
	</span>
	<span class="music-stave pointer-events-none block">
		{#if svg}
			{@html svg}
		{:else}
			<span class="grid place-items-center text-xs text-stone-600" style:min-height="{STAVE_BOX_HEIGHT}px">{engravingFailed ? 'Notation unavailable' : 'Loading notation…'}</span>
		{/if}
	</span>
</button>

<style>
	.music-stave :global(svg) {
		display: block;
		width: 100%;
		height: auto;
		overflow: visible;
	}
</style>
