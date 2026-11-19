<script lang="ts">
	import type { Snippet } from 'svelte';

	/** Keyboard-shortcut dialog shared by every game; `tone` keeps each game's visual language. */
	let {
		items,
		onclose,
		tone = 'indigo',
		closeOnEscape = true,
		closeOnQuestion = false,
		footer
	}: {
		items: { keys: string[]; action: string }[];
		onclose: () => void;
		tone?: 'indigo' | 'tesla';
		closeOnEscape?: boolean;
		closeOnQuestion?: boolean;
		footer?: Snippet;
	} = $props();

	let closeBtn: HTMLButtonElement | undefined = $state();
	$effect(() => {
		closeBtn?.focus();
	});

	const tesla = $derived(tone === 'tesla');

	function onKeyDown(e: KeyboardEvent) {
		if (!closeOnEscape && !closeOnQuestion) return;
		if ((closeOnEscape && e.key === 'Escape') || (closeOnQuestion && e.key === '?')) {
			if (closeOnQuestion) e.preventDefault();
			onclose();
		}
	}
</script>

	<svelte:window onkeydown={onKeyDown} />
{#if tesla}
	<div class="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4" role="presentation" onclick={(e) => e.target === e.currentTarget && onclose()}>
		<div role="dialog" aria-modal="true" aria-labelledby="shortcuts-title" class="w-full max-w-sm rounded-2xl bg-white p-6 shadow-xl" data-testid="shortcuts-dialog">
			<div class="mb-4 flex items-center justify-between">
				<h2 id="shortcuts-title" class="text-lg font-light text-[#171a20]">Keyboard shortcuts</h2>
				<button bind:this={closeBtn} type="button" onclick={onclose} class="text-sm text-gray-500 hover:text-gray-900" aria-label="Close shortcuts">✕</button>
			</div>
			<dl class="space-y-2 text-sm">
				{#each items as s (`${s.keys.join('|')}|${s.action}`)}
					<div class="flex items-center justify-between gap-4">
						<dt class="flex gap-1">
							{#each s.keys as k (k)}<kbd class="inline-flex h-6 min-w-[1.5rem] items-center justify-center rounded-md border border-gray-300 bg-gray-50 px-1.5 text-xs font-semibold text-gray-700">{k}</kbd>{/each}
						</dt>
						<dd class="text-right text-gray-600">{s.action}</dd>
					</div>
				{/each}
			</dl>
			{#if footer}
				<div class="mt-4 text-xs text-gray-500">{@render footer()}</div>
			{/if}
		</div>
	</div>
{:else}
	<div class="fixed inset-0 z-50 flex items-center justify-center bg-indigo-950/40 p-4 dark:bg-black/60" role="presentation" onclick={(e) => e.target === e.currentTarget && onclose()}>
		<div role="dialog" aria-modal="true" aria-labelledby="shortcuts-title" class="w-full max-w-sm rounded-2xl bg-white p-5 shadow-xl" data-testid="shortcuts-dialog">
			<div class="flex items-center justify-between">
				<h2 id="shortcuts-title" class="text-lg font-semibold text-indigo-950">Keyboard shortcuts</h2>
				<button bind:this={closeBtn} type="button" class="rounded-full px-2 text-lg leading-none text-slate-500 hover:bg-slate-100" aria-label="Close" onclick={onclose}>×</button>
			</div>
			<dl class="mt-3 grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1.5 text-sm">
				{#each items as s (`${s.keys.join('|')}|${s.action}`)}
					<dt><kbd class="rounded border border-indigo-200 bg-indigo-50 px-1.5 font-mono text-xs">{s.keys.join(' / ')}</kbd></dt>
					<dd class="text-slate-700">{s.action}</dd>
				{/each}
			</dl>
			{#if footer}
				<div class="mt-3 text-xs text-slate-600">{@render footer()}</div>
			{/if}
		</div>
	</div>
{/if}
