<script lang="ts">
	let { label, height = '8rem' }: { label: string; height?: string } = $props();
</script>

<div class="placeholder db-panel" role="status" style:min-height={height}>
	<div class="lines" aria-hidden="true"><span></span><span></span><span></span></div>
	<p>{label}</p>
</div>

<style>
	.placeholder { display: grid; place-content: center; justify-items: center; gap: .7rem; padding: 1rem; color: var(--db-muted); font-size: .875rem; }
	.lines { display: grid; gap: .35rem; width: 7rem; }
	.lines span { height: .4rem; border-radius: 1rem; background: var(--db-tint); }
	.lines span:nth-child(2) { width: 78%; }
	.lines span:nth-child(3) { width: 53%; }
</style>
