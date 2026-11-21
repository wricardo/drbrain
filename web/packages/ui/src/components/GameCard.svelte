<script lang="ts">
	import type { GameInfo } from '../catalog';
	import Button from './Button.svelte';
	let { game }: { game: GameInfo } = $props();
</script>

<article data-game={game.id} class="db-panel db-lift">
	<a href="/{game.id}" class="art" tabindex="-1" aria-hidden="true"><img src={game.art} alt="" width="756" height="336" loading="lazy" /></a>
	<div class="card-copy">
		<p class="tag">{game.tag}</p>
		<h3><a href="/{game.id}">{game.name}</a></h3>
		<p class="description">{game.description}</p>
		<Button href="/{game.id}" variant="secondary" aria-label="Play {game.name}">Play <span aria-hidden="true">→</span></Button>
	</div>
</article>

<style>
	article { display: flex; flex-direction: column; height: 100%; border-radius: var(--db-radius-lg); }
	@media (hover: hover) { article:hover { transform: translateY(-2px); box-shadow: var(--db-shadow-raised); } }
	.art { display: block; overflow: hidden; border-radius: calc(var(--db-radius-lg) - 1px) calc(var(--db-radius-lg) - 1px) 0 0; background: var(--db-accent-wash); }
	img { display: block; width: 100%; height: auto; aspect-ratio: 2.3; object-fit: cover; }
	.card-copy { display: flex; flex: 1; flex-direction: column; align-items: flex-start; gap: .5rem; padding: clamp(1rem, 2vw, 1.4rem); }
	h3 { color: var(--db-ink); font-size: clamp(1.22rem, 1.65vw, 1.5rem); font-weight: 710; line-height: 1.14; letter-spacing: -.035em; }
	h3 a:hover { text-decoration: underline; }
	.tag { background: var(--db-accent-wash); color: var(--db-accent); border-radius: 5px; padding: .22rem .46rem; font-size: .71rem; font-weight: 670; }
	.description { color: var(--db-muted); font-size: .91rem; line-height: 1.5; padding-bottom: .45rem; }
	.card-copy :global(.db-button) { margin-top: auto; }
</style>
