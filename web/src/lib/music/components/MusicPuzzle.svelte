<script lang="ts">
	import MeasureCard from './MeasureCard.svelte';
	import type { Session } from '../types';

	let {
		session,
		playing,
		busy,
		onplay,
		onstop,
		onswitch,
		ontransform,
		onreset
	}: {
		session: Session;
		playing: boolean;
		busy: boolean;
		onplay: (positions: number[]) => void;
		onstop: () => void;
		onswitch: (a: number, b: number) => void;
		ontransform: (positions: number[], axis: 'h' | 'v') => void;
		onreset: () => void;
	} = $props();

	let selected = $state<number[]>([]);
	let lastSession = '';

	// Selection is client-local and belongs to one session; a new puzzle starts clean.
	$effect(() => {
		const id = session.id;
		if (id !== lastSession) {
			lastSession = id;
			selected = [];
		}
	});

	const slots = $derived([...session.slots].sort((a, b) => a.position - b.position));
	const measures = $derived(new Map(session.excerpt.measures.map((m) => [m.id, m])));
	const canSwitch = $derived(selected.length === 2);
	const canFlip = $derived(selected.length > 0);

	function toggle(position: number) {
		selected = selected.includes(position) ? selected.filter((p) => p !== position) : [...selected, position];
	}

	function selectAll() {
		selected = slots.map((s) => s.position);
	}

	function clear() {
		selected = [];
	}

	function switchSelection() {
		if (selected.length === 2) onswitch(selected[0], selected[1]);
	}
</script>

<section class="db-panel min-w-0 p-3 sm:p-4" aria-label="Measure arrangement">
	<div class="score-toolbar">
		<div class="flex flex-wrap items-center justify-between gap-2">
			<div class="flex flex-wrap items-center gap-2 text-xs">
				<button
					type="button"
					class="min-h-11 rounded-lg border border-amber-400 bg-white px-3 py-1 font-medium text-amber-900 hover:bg-amber-50"
					onclick={selectAll}
					data-testid="select-all">Select All</button
				>
				<button
					type="button"
					class="min-h-11 rounded-lg border border-amber-400 bg-white px-3 py-1 font-medium text-amber-900 hover:bg-amber-50"
					onclick={clear}
					data-testid="clear">Clear</button
				>
				<span class="text-amber-800" data-testid="selection-count">{selected.length} selected</span>
			</div>
			<p class="text-xs text-amber-800/80">Select measures to play, switch or flip.</p>
		</div>

		<div class="flex flex-wrap items-center gap-2">
			<button
				type="button"
				class="min-h-11 rounded-lg bg-amber-700 px-5 py-2 text-sm font-semibold text-white hover:bg-amber-800 disabled:opacity-50"
				onclick={() => (playing ? onstop() : onplay(selected))}
				disabled={busy}
				data-testid="play">{playing ? 'Stop' : 'Play'}</button
			>
			<button
				type="button"
				class="min-h-11 rounded-lg border border-amber-500 bg-white px-4 py-2 text-sm font-semibold text-amber-900 hover:bg-amber-50 disabled:opacity-40"
				onclick={switchSelection}
				disabled={!canSwitch || busy}
				data-testid="switch">Switch</button
			>
			<button
				type="button"
				class="min-h-11 rounded-lg border border-amber-500 bg-white px-4 py-2 text-sm font-semibold text-amber-900 hover:bg-amber-50 disabled:opacity-40"
				onclick={() => ontransform(selected, 'h')}
				disabled={!canFlip || busy}
				data-testid="flip-h">H Flip</button
			>
			<button
				type="button"
				class="min-h-11 rounded-lg border border-amber-500 bg-white px-4 py-2 text-sm font-semibold text-amber-900 hover:bg-amber-50 disabled:opacity-40"
				onclick={() => ontransform(selected, 'v')}
				disabled={!canFlip || busy}
				data-testid="flip-v">V Flip</button
			>
			<button
				type="button"
				class="min-h-11 rounded-lg border border-stone-400 bg-white px-4 py-2 text-sm font-semibold text-stone-700 hover:bg-stone-50 disabled:opacity-40"
				onclick={onreset}
				disabled={busy}
				data-testid="reset">Reset</button
			>
			<span class="ml-auto text-xs text-amber-900/80">
				{session.difficulty} · {session.excerpt.timeSig} · {session.excerpt.key} · ♩={session.excerpt.tempoBPM}
			</span>
		</div>
	</div>
	<div class="flex min-w-0 flex-wrap items-stretch gap-2" role="group" aria-label="Measures">
		{#each slots as slot (slot.position)}
			{@const measure = measures.get(slot.measureID)}
			{#if measure}
				<MeasureCard
					{measure}
					flipH={slot.flipH}
					flipV={slot.flipV}
					axis={session.excerpt.axis}
					keySig={session.excerpt.key}
					timeSig={session.excerpt.timeSig}
					clef={session.excerpt.clef}
					position={slot.position}
					selected={selected.includes(slot.position)}
					onToggle={toggle}
				/>
			{/if}
		{/each}
	</div>

	{#if session.solved}
		<div
			role="status"
			class="mt-3 rounded-xl border-2 border-emerald-500 bg-emerald-50 px-4 py-3 text-sm font-semibold text-emerald-900"
			data-testid="solved"
		>
			Solved! {session.wins} {session.wins === 1 ? 'win' : 'wins'} · +{session.percentPerWin}% per win
		</div>
	{/if}
</section>

<style>
	.score-toolbar { display: grid; gap: .55rem; margin-bottom: .8rem; }
	@media (max-width: 760px) {
		.score-toolbar { position: sticky; top: 0; z-index: 10; padding: .6rem 0; background: var(--db-surface); border-bottom: 1px solid var(--db-line); }
	}
</style>
