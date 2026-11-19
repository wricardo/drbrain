<script lang="ts">
	import type { ConnectionState } from '../session';

	/** Live-update indicator shared by every game; tone picks the game's border/ink colours. */
	let { state, tone = 'indigo' }: { state: ConnectionState; tone?: 'indigo' | 'tesla' } = $props();

	const text: Record<ConnectionState, string> = {
		connecting: 'Connecting…',
		live: 'Live',
		reconnecting: 'Reconnecting…',
		offline: 'Offline'
	};
	const dot: Record<ConnectionState, string> = {
		connecting: 'bg-amber-500',
		live: 'bg-emerald-500',
		reconnecting: 'bg-amber-500 animate-pulse',
		offline: 'bg-rose-500'
	};
	const shell = $derived(
		tone === 'tesla'
			? 'border-[#e8e8e8] text-[#393c41]'
			: 'border-indigo-200 text-indigo-900'
	);
</script>

<span
	class="inline-flex items-center gap-1.5 rounded-full border bg-white px-2.5 py-1 text-xs font-medium {shell}"
	role="status"
	title="Live updates connection"
	data-testid="connection"
>
	<span class="h-2 w-2 rounded-full {dot[state]}" aria-hidden="true"></span>
	{text[state]}
</span>
