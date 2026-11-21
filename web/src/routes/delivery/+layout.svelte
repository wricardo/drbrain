<script lang="ts">
	import GameNav from '@drbrain/ui/components/GameNav.svelte';
	import { games } from '@drbrain/ui/catalog';
	import { page } from '$app/state';

	const game = games.find((item) => item.id === 'delivery')!;
	const tabs = [
		{ href: '/delivery', label: 'Play' },
		{ href: '/delivery/cities', label: 'Cities' },
		{ href: '/delivery/editor', label: 'Editor' }
	];

	function isActive(href: string, pathname: string): boolean {
		if (href === '/delivery') return pathname === '/delivery';
		return pathname === href || pathname.startsWith(`${href}/`);
	}

	let { children } = $props();
</script>

<div class="suite-game tabular-nums" data-game="delivery">
	<GameNav {game} {tabs} active={(href) => isActive(href, page.url.pathname)} />
	{@render children()}
</div>
