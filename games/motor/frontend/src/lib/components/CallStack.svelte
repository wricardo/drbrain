<script lang="ts">
	import { tapeLabel } from '$lib/instructions';
	import { blockedCallNotice, recursionRule } from '$lib/recursion';
	import type { Frame, GameMap, StepEvent } from '$lib/types';

	let {
		callStack,
		map,
		lastEvent = null
	}: { callStack: Frame[]; map: Pick<GameMap, 'maxCallDepth' | 'allowRecursion'>; lastEvent?: StepEvent | null } = $props();

	const maxCallDepth = $derived(map.maxCallDepth);
	// CallStack[0] is always main; depth counts calls in flight.
	const depth = $derived(Math.max(0, callStack.length - 1));
	const pct = $derived(maxCallDepth > 0 ? Math.min(100, (depth / maxCallDepth) * 100) : 0);
	const frames = $derived(callStack.map((f, i) => ({ ...f, i })).reverse());
	const rule = $derived(recursionRule(map));
	const notice = $derived(blockedCallNotice(lastEvent));
</script>

<section aria-labelledby="callstack-h" class="rounded-xl border border-indigo-200 bg-white p-3">
	<h3 id="callstack-h" class="text-sm font-semibold text-indigo-900 flex justify-between">
		<span>Call stack</span>
		<span data-testid="depth">depth {depth} / {maxCallDepth}</span>
	</h3>
	<div
		class="mt-2 h-2 rounded-full bg-indigo-100 overflow-hidden"
		role="meter"
		aria-label="Call depth"
		aria-valuemin="0"
		aria-valuemax={maxCallDepth}
		aria-valuenow={depth}
	>
		<div class="h-full {pct >= 80 ? 'bg-rose-500' : 'bg-indigo-500'}" style="width: {pct}%"></div>
	</div>
	<ol class="mt-2 space-y-1 text-xs font-mono max-h-48 overflow-auto" aria-label="Frames, top first">
		{#each frames as f (f.i)}
			<li class="flex justify-between rounded px-2 py-1 {f.i === callStack.length - 1 ? 'bg-indigo-100 text-indigo-900' : 'bg-slate-50 text-slate-600'}">
				<span>{tapeLabel(f.tape)}{f.i === callStack.length - 1 ? ' (running)' : ''}</span>
				<span>{f.i === callStack.length - 1 ? 'next' : 'returns after'} slot {f.i === callStack.length - 1 ? f.pc + 1 : f.pc}</span>
			</li>
		{/each}
	</ol>
	<p class="mt-2 text-xs text-slate-600" data-testid="recursion-rule">{rule}</p>
	{#if notice}
		<p class="mt-2 rounded-lg bg-amber-50 px-2 py-1.5 text-xs text-amber-900" role="status" data-testid="blocked-call">
			{notice}
		</p>
	{/if}
</section>
