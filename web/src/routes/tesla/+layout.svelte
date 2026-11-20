<script lang="ts">
	import favicon from '$lib/tesla/assets/favicon.svg';
	import '$lib/tesla/app.css';
	import { setContextClient } from '@urql/svelte';
	import { makeClient } from '$lib/tesla/graphql';
	import { browser } from '$app/environment';
	import { page } from '$app/stores';
	import AccountMenu from '$lib/motor/components/AccountMenu.svelte';

	let { children } = $props();

	const navItems = [
		{ href: '/tesla', label: 'Play' },
		{ href: '/tesla/maps', label: 'Maps' },
		{ href: '/tesla/lobby', label: 'Sessions' },
		{ href: '/tesla/learn', label: 'How it works' },
		{ href: '/tesla/multi', label: 'Watch multiple' }
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

<svelte:head>
	<link rel="icon" href={favicon} />
	<link rel="preconnect" href="https://fonts.googleapis.com" />
	<link href="https://fonts.googleapis.com/css2?family=Inter:wght@300;400;500;600&display=swap" rel="stylesheet" />
</svelte:head>

<div class="min-h-screen flex flex-col bg-[#f7f7f7] text-[#393c41]" style="font-family: 'Inter', system-ui, sans-serif">
	<header class="border-b border-[#e8e8e8] bg-white px-6 py-4 flex items-center justify-between">
		<a href="/tesla" class="flex items-center gap-3 no-underline">
			<span class="text-xl font-light tracking-widest text-[#393c41]">TESLA</span>
			<span class="text-xs text-gray-400 font-light">Road Trip</span>
		</a>
		<nav class="flex items-center gap-1 text-sm" aria-label="Main">
			{#each navItems as item}
				{@const active = isActive(item.href, $page.url.pathname)}
				<a
					href={item.href}
					aria-current={active ? 'page' : undefined}
					class="px-3 py-1.5 rounded-full transition-colors {active ? 'bg-[#393c41] text-white' : 'text-gray-500 hover:text-[#393c41] hover:bg-gray-100'}"
				>{item.label}</a>
			{/each}
			<AccountMenu />
		</nav>
	</header>

	<main class="flex-1">
		{@render children()}
	</main>

	<footer class="border-t border-[#e8e8e8] bg-white px-6 py-8 mt-auto">
		<div class="max-w-7xl mx-auto grid grid-cols-2 sm:grid-cols-4 gap-6 text-xs text-gray-400">
			<div>
				<p class="font-semibold text-[#393c41] mb-2">Tesla Road Trip</p>
				<p class="leading-relaxed">Teach AI agents to navigate, plan routes, and manage resources through an interactive educational game.</p>
			</div>
			<div>
				<p class="font-semibold text-[#393c41] mb-2">Play</p>
				<div class="flex flex-col gap-1.5">
					<a href="/tesla" class="hover:text-gray-600 transition-colors">Home</a>
					<a href="/tesla/maps" class="hover:text-gray-600 transition-colors">Maps</a>
					<a href="/tesla/lobby" class="hover:text-gray-600 transition-colors">Sessions</a>
					<a href="/tesla/multi" class="hover:text-gray-600 transition-colors">Watch multiple</a>
					<a href="/tesla/editor" class="hover:text-gray-600 transition-colors">Map editor</a>
				</div>
			</div>
			<div>
				<p class="font-semibold text-[#393c41] mb-2">Docs</p>
				<div class="flex flex-col gap-1.5">
					<a href="/tesla/learn" class="hover:text-gray-600 transition-colors">How it works</a>
					<a href="/tesla/llms.txt" target="_blank" rel="noreferrer" class="hover:text-gray-600 transition-colors">/llms.txt</a>
					<a href="/graphql" target="_blank" rel="noreferrer" class="hover:text-gray-600 transition-colors">GraphQL endpoint</a>
				</div>
			</div>
			<div>
				<p class="font-semibold text-[#393c41] mb-2">Tools</p>
				<div class="flex flex-col gap-1.5">
					<a href="/graphql/playground" target="_blank" rel="noreferrer" class="hover:text-gray-600 transition-colors">GraphQL Playground</a>
					<a href="/tesla/admin" class="hover:text-gray-600 transition-colors">Admin</a>
					<a href="https://github.com/wricardo/gqlcli#-quick-start--using-the-cli" target="_blank" rel="noreferrer" class="hover:text-gray-600 transition-colors">gqlcli — GraphQL CLI</a>
				</div>
			</div>
		</div>
	</footer>
</div>
