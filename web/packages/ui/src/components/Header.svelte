<script lang="ts">
	import type { Snippet } from 'svelte';
	import Brand from './Brand.svelte';
	import { games, type GameInfo } from '../catalog';

	let { current, account }: { current?: GameInfo; account?: Snippet } = $props();
	let open = $state(false);
	let menu: HTMLDivElement;
	let trigger: HTMLButtonElement;
</script>

<svelte:window
	onclick={(event) => { if (open && !menu?.contains(event.target as Node)) open = false; }}
	onkeydown={(event) => { if (open && event.key === 'Escape') { open = false; trigger?.focus(); } }}
/>

<header>
	<div class="db-container bar">
		<Brand />
		<div class="switcher" bind:this={menu}>
			<button type="button" bind:this={trigger} class="switch" aria-expanded={open} onclick={() => (open = !open)}>
				<span class="tiles" aria-hidden="true"><i></i><i></i><i></i><i></i></span>
				<span class="wide">Games</span><span class="narrow">Menu</span>
				<span class="chevron" aria-hidden="true"></span>
			</button>
			{#if open}
				<nav aria-label="Switch games" class="dropdown db-panel">
					<a href="/#games" class="overview" onclick={() => (open = false)}>All games <span aria-hidden="true">↗</span></a>
					{#each games as game (game.id)}
						<a href="/{game.id}" data-game={game.id} aria-current={current?.id === game.id ? 'true' : undefined} onclick={() => (open = false)}>
							<span class="game-dot" aria-hidden="true"></span>{game.name}
						</a>
					{/each}
				</nav>
			{/if}
		</div>
		{#if current}<a class="current" href="/{current.id}" data-game={current.id}>{current.name}</a>{/if}
		<div class="account">{@render account?.()}</div>
	</div>
</header>

<style>
	header { position: relative; z-index: 40; border-bottom: 1px solid var(--db-line); background: var(--db-surface); }
	.bar { display: flex; min-height: 68px; align-items: center; gap: clamp(.6rem, 2vw, 1.4rem); }
	.switcher { position: relative; }
	.switch { display: flex; align-items: center; min-height: 44px; padding: 0 .65rem; gap: .55rem; border-radius: 8px; color: var(--db-ink); font-weight: 650; font-size: .875rem; cursor: pointer; }
	.switch:hover, .switch[aria-expanded='true'] { background: var(--db-tint); }
	.tiles { display: grid; grid-template-columns: repeat(2, 5px); gap: 2px; color: var(--db-brand); }
	.tiles i { width: 5px; height: 5px; border-radius: 1px; background: currentColor; }
	.chevron { width: 6px; height: 6px; border-right: 1.5px solid; border-bottom: 1.5px solid; transform: rotate(45deg) translateY(-2px); }
	.narrow { display: none; }
	.current { border-left: 1px solid var(--db-line); padding-left: 1.1rem; color: var(--db-accent); font-weight: 680; font-size: .875rem; }
	.current:hover { text-decoration: underline; }
	.account { margin-inline-start: auto; min-width: 0; }
	.dropdown { position: absolute; top: calc(100% + .6rem); left: 0; width: 232px; padding: .4rem; box-shadow: var(--db-shadow-raised); }
	.dropdown a { display: flex; min-height: 44px; align-items: center; gap: .65rem; padding: .45rem .7rem; border-radius: 7px; color: var(--db-ink); font-size: .875rem; }
	.dropdown a:hover, .dropdown a[aria-current] { background: var(--db-tint); }
	.dropdown .overview { justify-content: space-between; color: var(--db-brand); font-weight: 700; }
	.game-dot { width: 8px; height: 8px; border-radius: 50%; background: var(--db-accent); }
	@media (max-width: 660px) {
		.bar { min-height: 62px; }
		.current, .wide { display: none; }
		.narrow { display: inline; }
	}
	@media (max-width: 460px) { .dropdown { left: -4rem; } }
</style>
