<script lang="ts">
	import type { Snippet } from 'svelte';
	import type { GameInfo } from '../catalog';
	let { game, tabs = [], active, children }: {
		game: GameInfo; tabs?: { href: string; label: string }[]; active?: (href: string) => boolean; children?: Snippet;
	} = $props();
</script>

<div class="strip">
	<div class="db-container context">
		<a class="back" href="/#games"><span aria-hidden="true">←</span> All games</a>
		<span class="separator" aria-hidden="true"></span>
		<a class="game" href="/{game.id}">{game.name}</a>
		{#if children}<div class="extras">{@render children()}</div>{/if}
		{#if tabs.length}
			<nav aria-label="{game.name} navigation">
				{#each tabs as tab (tab.href)}
					<a href={tab.href} aria-current={active?.(tab.href) ? 'page' : undefined}>{tab.label}</a>
				{/each}
			</nav>
		{/if}
	</div>
</div>

<style>
	.strip { background: var(--db-page); border-bottom: 1px solid var(--db-line); }
	.context { display: flex; flex-wrap: wrap; align-items: center; gap: 0 .65rem; min-height: 53px; font-size: .83rem; }
	.back, .game { display: inline-flex; min-height: 44px; align-items: center; gap: .35rem; white-space: nowrap; }
	.back { color: var(--db-muted); }
	.game { color: var(--db-accent); font-weight: 740; }
	.back:hover, .game:hover { text-decoration: underline; }
	.separator { height: 1rem; width: 1px; background: var(--db-line); }
	.extras { order: 1; margin-inline-start: auto; }
	nav { display: flex; align-items: center; gap: .2rem; min-width: 0; margin-inline-start: auto; }
	nav a { display: inline-flex; align-items: center; min-height: 44px; border-radius: 7px; padding-inline: .68rem; color: var(--db-muted); font-weight: 600; white-space: nowrap; }
	nav a:hover, nav a[aria-current] { color: var(--db-accent); background: var(--db-accent-wash); }
	@media (min-width: 661px) { .separator, .game { display: none; } }
	@media (max-width: 1100px) {
		nav { order: 2; width: 100%; overflow-x: auto; padding: 5px; margin: 0 -5px; overscroll-behavior-inline: contain; }
		nav a { padding-inline: .58rem; }
	}
</style>
