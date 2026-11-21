<script lang="ts">
	import type { Snippet } from 'svelte';
	let { title, accent, description, label, image, wideArt = false, children }: {
		title: string; accent?: string; label?: string; description: string; image?: string; wideArt?: boolean; children?: Snippet;
	} = $props();
</script>

<section class="hero" class:has-art={!!image} class:wide-art={wideArt}>
	<div class="copy">
		{#if label}<p class="db-kicker">{label}</p>{/if}
		<h1 class="db-heading">{title}{#if accent} <em>{accent}</em>{/if}</h1>
		<p class="description">{description}</p>
		{#if children}<div class="actions">{@render children()}</div>{/if}
	</div>
	{#if image}<img src={image} alt="" width={wideArt ? 768 : 1080} height={wideArt ? 341 : 720} fetchpriority="high" />{/if}
</section>

<style>
	.hero { position: relative; border: 1px solid var(--db-line); border-radius: var(--db-radius-lg); background: var(--db-surface); overflow: hidden; box-shadow: var(--db-shadow); }
	.has-art { display: grid; grid-template-columns: 1fr 1fr; }
	.copy { padding: clamp(1.65rem, 4.1vw, 4rem); }
	h1 { color: var(--db-ink); margin-top: .6rem; text-wrap: initial; }
	em { display: block; font-family: var(--db-serif); font-weight: 400; color: var(--db-accent); letter-spacing: -.066em; }
	.description { margin-top: 1rem; max-width: 50ch; color: var(--db-muted); font-size: clamp(.98rem, 1.25vw, 1.125rem); line-height: 1.6; }
	.actions { margin-top: 1.4rem; display: flex; gap: .65rem; flex-wrap: wrap; }
	img { width: 100%; height: 100%; min-height: 0; object-fit: cover; background-color: var(--db-accent-wash); }
	@media (max-width: 760px) {
		.has-art { grid-template-columns: 1fr; }
		img { height: auto; aspect-ratio: 1.5; }
		.wide-art img { aspect-ratio: 2.25; }
	}
</style>
