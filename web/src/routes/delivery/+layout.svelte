<script lang="ts">
	import '$lib/delivery/app.css';
	import AccountMenu from '$lib/motor/components/AccountMenu.svelte';

	let { children } = $props();
</script>

<div class="h-screen bg-[var(--color-ink-950)] text-[var(--color-fog-300)] font-sans tabular-nums">
	<div class="fixed right-3 top-3 z-50"><AccountMenu /></div>
	{@render children()}
</div>
