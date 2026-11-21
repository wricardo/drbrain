<script lang="ts">
	import type { Snippet } from 'svelte';
	import type { HTMLAnchorAttributes, HTMLButtonAttributes } from 'svelte/elements';

	type Props = { variant?: 'primary' | 'secondary' | 'quiet'; children: Snippet } & (
		(HTMLAnchorAttributes & { href: string }) | (HTMLButtonAttributes & { href?: undefined })
	);
	let { href, variant = 'primary', children, class: className = '', ...attrs }: Props = $props();
</script>

{#if href !== undefined}
	<a {...attrs as HTMLAnchorAttributes} {href} class="db-button {className}" data-variant={variant}>{@render children()}</a>
{:else}
	<button type="button" {...attrs as HTMLButtonAttributes} class="db-button {className}" data-variant={variant}>{@render children()}</button>
{/if}
