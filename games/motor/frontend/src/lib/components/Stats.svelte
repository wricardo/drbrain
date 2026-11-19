<script lang="ts">
	import type { GameMap, VMState } from '$lib/types';

	/** A live session, or the zeroed preview of a map that has no session yet (/map). */
	type StatsInput = {
		map: Pick<GameMap, 'treats' | 'maxSteps'>;
		vm: Pick<VMState, 'steps' | 'treatsRemaining'>;
		bestSteps: number | null;
		attempts: number;
	};

	let { session }: { session: StatsInput } = $props();

	const total = $derived(session.map.treats.length);
	const collected = $derived(total - session.vm.treatsRemaining.length);
	const stepPct = $derived(Math.min(100, (session.vm.steps / Math.max(1, session.map.maxSteps)) * 100));
	const cell = 'rounded-lg border border-indigo-200 bg-white px-2.5 py-1.5';
	// Phones get one wrapped line of "label value" pairs instead of four cards, which cost ~40px of height.
	const pair = `${cell} max-sm:flex max-sm:items-baseline max-sm:gap-1 max-sm:rounded-none max-sm:border-0 max-sm:bg-transparent max-sm:p-0`;
</script>

<!-- Same compact row on play and watch: progress, goal, best, runs ("value / limit" for bounded counters). -->
<dl class="flex flex-wrap items-baseline gap-x-3 gap-y-0.5 text-xs sm:grid sm:grid-cols-4 sm:gap-2 sm:text-sm" data-testid="stats">
	<div class={pair}>
		<dt class="text-xs text-slate-600">Steps</dt>
		<dd class="font-semibold tabular-nums" data-testid="steps">{session.vm.steps} / {session.map.maxSteps}</dd>
		<div class="mt-1 h-1 rounded-full bg-indigo-100 overflow-hidden max-sm:hidden" aria-hidden="true">
			<div class="h-full {stepPct >= 80 ? 'bg-rose-500' : 'bg-indigo-500'}" style="width: {stepPct}%"></div>
		</div>
	</div>
	<div class={pair}>
		<dt class="text-xs text-slate-600">Treats</dt>
		<dd class="font-semibold tabular-nums" data-testid="treats">{collected} / {total}</dd>
	</div>
	<div class={pair}>
		<dt class="text-xs text-slate-600">Best</dt>
		<dd class="font-semibold tabular-nums" data-testid="best">{session.bestSteps != null ? `${session.bestSteps} steps` : '—'}</dd>
	</div>
	<div class={pair}>
		<dt class="text-xs text-slate-600">Runs</dt>
		<dd class="font-semibold tabular-nums" data-testid="attempts">{session.attempts}</dd>
	</div>
</dl>
