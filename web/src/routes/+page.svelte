<script lang="ts">
	import { games } from '@drbrain/ui/catalog';
	import Button from '@drbrain/ui/components/Button.svelte';
	import GameCard from '@drbrain/ui/components/GameCard.svelte';
	import Hero from '@drbrain/ui/components/Hero.svelte';
	import { account } from '$lib/account/stores/account';
</script>

<svelte:head><title>drbrain · A playground for curious minds</title></svelte:head>

<div class="db-container home">
	<Hero
		label="Browser puzzle games"
		title="A playground for"
		accent="curious minds."
		description="Explore five games that turn route planning, programming and pattern-finding into play."
		image="/art/hero.webp"
	>
		<Button href="#games">Play now <span aria-hidden="true">↓</span></Button>
		{#if $account.me}
			<Button href="/settings" variant="secondary">My account</Button>
		{:else}
			<Button href="/signup" variant="secondary">Sign up</Button>
		{/if}
	</Hero>

	<section id="games" aria-labelledby="games-title">
		<div class="section-title">
			<h2 id="games-title">Explore the games</h2>
			<p>Play directly in your browser.</p>
		</div>
		<div class="catalog">
			{#each games as game (game.id)}<GameCard {game} />{/each}
		</div>
	</section>

	<section class="why" aria-labelledby="why-title" id="developers">
		<h2 id="why-title">Why drbrain?</h2>
		<div class="why-grid">
			<div>
				<h3>One account across all five games</h3>
				<p>Jump into a game as a guest, or sign in with a single account. Your Motor Programming runs can appear on your profile.</p>
				{#if !$account.me}<a class="db-text-link" href="/signup">Create an account <span aria-hidden="true">↗</span></a>{/if}
			</div>
			<div>
				<h3>Play with an AI agent</h3>
				<p>Use the shared GraphQL API to control a game programmatically. Tesla Road Trip also offers an MCP endpoint.</p>
				<a class="db-text-link" href="/tesla/llms.txt" data-sveltekit-reload>Read the agent guide <span aria-hidden="true">↗</span></a>
			</div>
		</div>
	</section>
</div>

<style>
	.home { padding-block: clamp(1rem, 3vw, 2.55rem) clamp(3rem, 7vw, 7rem); }
	section { scroll-margin-top: 1rem; }
	#games { margin-top: clamp(2.15rem, 4vw, 4rem); }
	.section-title { display: flex; flex-wrap: wrap; gap: .35rem 1.5rem; align-items: baseline; justify-content: space-between; margin-bottom: 1.15rem; }
	h2 { font-size: clamp(1.65rem, 3vw, 2.45rem); font-weight: 710; letter-spacing: -.052em; line-height: 1.12; }
	.section-title p, .why p { color: var(--db-muted); font-size: .95rem; }
	.catalog { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: clamp(1rem, 2vw, 1.45rem); }
	.why { margin-top: clamp(2.75rem, 6vw, 5.5rem); padding: clamp(1.5rem, 5vw, 3.75rem); background: var(--db-tint); border-radius: var(--db-radius-lg); }
	.why-grid { display: grid; gap: 1.65rem clamp(2rem, 7vw, 7rem); grid-template-columns: 1fr 1fr; margin-top: 1.25rem; }
	.why h3 { font-size: 1.12rem; font-weight: 710; letter-spacing: -.025em; }
	.why p { margin-top: .35rem; line-height: 1.6; max-width: 45ch; }
	.why a { display: inline-flex; align-items: center; min-height: 44px; gap: .35rem; font-weight: 650; font-size: .875rem; }
	@media (max-width: 880px) { .catalog { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
	@media (max-width: 550px) { .catalog, .why-grid { grid-template-columns: 1fr; } }
</style>
