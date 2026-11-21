<script lang="ts">
	import type { Snippet } from 'svelte';
	import Card from './Card.svelte';
	let { title, description, children }: { title: string; description: string; children?: Snippet } = $props();
</script>

<Card class="db-empty">
	<p class="title">{title}</p>
	<p class="body">{description}</p>
	{#if children}<div class="actions">{@render children()}</div>{/if}
</Card>

<style>
	:global(.db-empty) { padding: clamp(1.5rem, 5vw, 3rem); text-align: center; }
	.title { color: var(--db-ink); font-size: 1.23rem; font-weight: 700; }
	.body { max-width: 42ch; margin: .5rem auto 0; color: var(--db-muted); }
	.actions { margin-top: 1.25rem; }
</style>
