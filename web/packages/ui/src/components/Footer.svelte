<script lang="ts">
	import Brand from './Brand.svelte';
	import type { GameInfo } from '../catalog';
	let { current }: { current?: GameInfo } = $props();
</script>

<footer>
	<div class="db-container footer">
		<Brand />
		<nav aria-label="Footer">
			<a href="/#games">All games</a>
			<a href="/#developers">For developers</a>
			{#if current?.id === 'delivery'}
				<a href="/graphql" data-sveltekit-reload>GraphQL endpoint</a>
			{:else if current}
				<a href="/{current.id}/llms.txt" data-sveltekit-reload>Game API guide</a>
			{/if}
			{#if current?.id === 'tesla'}<a href="/tesla/privacy">Privacy</a>{/if}
		</nav>
	</div>
</footer>

<style>
	footer { border-top: 1px solid var(--db-line); background: var(--db-surface); }
	.footer { display: flex; justify-content: space-between; align-items: center; gap: .65rem 2rem; flex-wrap: wrap; min-height: 90px; padding-block: 1.1rem; }
	nav { display: flex; align-items: center; gap: .25rem 1.3rem; flex-wrap: wrap; color: var(--db-muted); font-size: .875rem; }
	nav a { display: inline-flex; align-items: center; min-height: 44px; }
	nav a:hover { color: var(--db-brand); text-decoration: underline; }
</style>
