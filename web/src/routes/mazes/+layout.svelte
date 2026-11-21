<script lang="ts">
	import { setContextClient } from '@urql/svelte';
	import { makeClient } from '$lib/mazes/graphql';
	import { browser } from '$app/environment';
	import { page } from '$app/stores';

	import GameNav from '@drbrain/ui/components/GameNav.svelte';
	import { games } from '@drbrain/ui/catalog';
	const game = games.find((item) => item.id === 'mazes')!;

	let { children } = $props();

	const navItems = [
		{ href: '/mazes', label: 'Play' },
		{ href: '/mazes/maps', label: 'Maps' },
		{ href: '/mazes/sessions', label: 'Sessions' },
		{ href: '/mazes/learn', label: 'How it works' },
		{ href: '/mazes/editor', label: 'Editor' }
	];

	/** Home and /play/* are "Play"; the multi-watch grid belongs to "Sessions". */
	function isActive(href: string, pathname: string): boolean {
		if (href === '/mazes') return pathname === '/mazes' || pathname.startsWith('/mazes/play/');
		if (href === '/mazes/sessions' && (pathname === '/mazes/multi' || pathname.startsWith('/mazes/multi/'))) return true;
		return pathname === href || pathname.startsWith(`${href}/`);
	}

	// ssr=false so this always runs in the browser; safe to set the client synchronously.
	if (browser) {
		setContextClient(makeClient());
	}
</script>

<div class="suite-game" data-game="mazes">
	<GameNav {game} tabs={navItems} active={(href) => isActive(href, $page.url.pathname)} />
	{#key $page.url.pathname}
		{@render children()}
	{/key}
</div>
