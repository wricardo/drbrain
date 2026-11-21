<script lang="ts">
	import type { GrowthView } from '$lib/delivery/api';
	import Panel from '$lib/delivery/Panel.svelte';

	interface Props {
		growth: GrowthView[];
		/** The clock tick right now, so the panel can say how far the next stage is. */
		tick: number;
	}

	let { growth, tick }: Props = $props();

	/**
	 * The city expands on its own timeline. Nothing here is a button: a stage
	 * applies when the session clock reaches its tick, so the panel shows what
	 * has happened and what is next rather than offering a purchase.
	 */
	const done = $derived(growth.filter((g) => g.done).length);
	const next = $derived(growth.find((g) => !g.done) ?? null);
</script>

<Panel title="City growth" summary="{done}/{growth.length} stages">
	<ul class="divide-y divide-edge">
		{#each growth as g (g.id)}
			<li class="px-3 py-2">
				<div class="flex items-baseline justify-between gap-2">
					<span class="text-sm {g.done ? 'text-body' : 'text-faint'}">{g.title}</span>
					{#if g.done}
						<span class="rounded bg-[var(--color-money)]/20 px-1.5 py-0.5 text-[11px] text-[var(--color-money)]"
							>open</span
						>
					{:else if next && g.id === next.id}
						<span class="text-xs text-[var(--color-charger)]"
							>in {Math.max(0, g.tick - tick)} tick{Math.max(0, g.tick - tick) === 1 ? '' : 's'}</span
						>
					{:else}
						<span class="text-xs text-faint">tick {g.tick}</span>
					{/if}
				</div>
				<p class="mt-0.5 text-xs text-faint">{g.description}</p>
			</li>
		{/each}
		{#if growth.length === 0}
			<li class="px-3 py-2 text-xs text-faint">This city has no scheduled growth.</li>
		{/if}
	</ul>
</Panel>
