<script lang="ts">
	let { message, kind = 'info', onclose }: { message: string; kind?: 'info' | 'error'; onclose?: () => void } = $props();
</script>

<div class="notice db-container">
	<div class="message" class:error={kind === 'error'}>
		<p role={kind === 'error' ? 'alert' : 'status'}>{message}</p>
		{#if onclose}<button type="button" aria-label="Dismiss" onclick={onclose}>×</button>{/if}
	</div>
</div>

<style>
	.notice { margin-block: .75rem; }
	.message { display: flex; align-items: center; justify-content: space-between; gap: 1rem; padding: .35rem .9rem; min-height: 44px; font-size: .875rem; border-radius: 10px; background: var(--db-tint); color: var(--db-ink); }
	.error { color: var(--db-danger); }
	button { min-width: 44px; min-height: 44px; font-size: 1.35rem; cursor: pointer; }
</style>
