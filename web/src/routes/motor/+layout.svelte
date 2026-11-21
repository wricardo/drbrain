<script lang="ts">
	import { page } from '$app/stores';
	import { onMount } from 'svelte';
	import { initTheme } from '$lib/motor/stores/theme';
	import { applyTheme } from '$lib/motor/theme';
	import ThemeToggle from '$lib/motor/components/ThemeToggle.svelte';
	import ClaimBanner from '$lib/motor/components/ClaimBanner.svelte';

	import GameNav from '@drbrain/ui/components/GameNav.svelte';
	import { games } from '@drbrain/ui/catalog';
	const game = games.find((item) => item.id === 'motor')!;

	let { children } = $props();

	const navItems = [
		{ href: '/motor', label: 'Play' },
		{ href: '/motor/maps', label: 'Maps' },
		{ href: '/motor/leaderboard', label: 'Leaderboard' },
		{ href: '/motor/runs', label: 'My runs' },
		{ href: '/motor/sessions', label: 'Sessions' },
		{ href: '/motor/learn', label: 'How it works' },
		{ href: '/motor/editor', label: 'Editor' }
	];

	function isActive(href: string, pathname: string): boolean {
		if (href === '/motor') return pathname === '/motor' || pathname.startsWith('/motor/play/');
		if (href === '/motor/sessions')
			return (
				pathname.startsWith('/motor/sessions') ||
				pathname.startsWith('/motor/watch/') ||
				pathname.startsWith('/motor/multi')
			);
		if (href === '/motor/maps') return pathname === '/motor/maps' || pathname.startsWith('/motor/map/');
		return pathname === href || pathname.startsWith(`${href}/`);
	}


	// Loads the saved theme and follows the OS setting while the choice is System.
	// Dark mode belongs to Motor only: every other game is light, so reset the
	// document when this layout is torn down (navigating out of /motor).
	onMount(() => {
		const stop = initTheme();
		return () => {
			stop();
			applyTheme('light');
		};
	});
</script>

<div class="suite-game" data-game="motor">
	<GameNav {game} tabs={navItems} active={(href) => isActive(href, $page.url.pathname)}><ThemeToggle /></GameNav>
	<ClaimBanner />
	{@render children()}
</div>
