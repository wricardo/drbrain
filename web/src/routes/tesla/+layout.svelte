<script lang="ts">
	import { setContextClient } from '@urql/svelte';
	import { makeClient } from '$lib/tesla/graphql';
	import { browser } from '$app/environment';
	import { page } from '$app/stores';

	import GameNav from '@drbrain/ui/components/GameNav.svelte';
	import { games } from '@drbrain/ui/catalog';
	const game = games.find((item) => item.id === 'tesla')!;

	let { children } = $props();

	const navItems = [
		{ href: '/tesla', label: 'Play' },
		{ href: '/tesla/maps', label: 'Maps' },
		{ href: '/tesla/lobby', label: 'Sessions' },
		{ href: '/tesla/learn', label: 'How it works' },
		{ href: '/tesla/multi', label: 'Watch multiple' },
		{ href: '/tesla/editor', label: 'Editor' },
		{ href: '/tesla/admin', label: 'Admin' }
	];

	// /tesla/watch/<id> is Tesla's play page (it has the drive controls), so it lights up Play.
	function isActive(href: string, pathname: string): boolean {
		return href === '/tesla'
			? pathname === '/tesla' || pathname.startsWith('/tesla/watch/')
			: pathname === href || pathname.startsWith(`${href}/`);
	}

	// ssr=false so this always runs in browser — safe to set client synchronously
	if (browser) {
		setContextClient(makeClient());
	}
</script>

<div class="suite-game" data-game="tesla">
	<GameNav {game} tabs={navItems} active={(href) => isActive(href, $page.url.pathname)} />
	{@render children()}
</div>
