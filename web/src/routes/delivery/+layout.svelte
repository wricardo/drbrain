<script lang="ts">
	import '$lib/delivery/app.css';

	let { children } = $props();
</script>

<div class="h-screen bg-[var(--color-ink-950)] text-[var(--color-fog-300)] font-sans tabular-nums">
	{@render children()}
</div>
