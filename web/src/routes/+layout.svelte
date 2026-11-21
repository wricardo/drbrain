<script lang="ts">
	import '../app.css';
	import { browser } from '$app/environment';
	import { page } from '$app/stores';
	import { goto } from '$app/navigation';
	import { setContextClient } from '@urql/svelte';
	import { makeClient } from '$lib/motor/graphql';
	import { signinNotice, withoutSigninParam } from '$lib/account/account';
	import { refreshAccount } from '$lib/account/stores/account';
	import { redeemSigninCode } from '$lib/account/token';
	import AccountMenu from '$lib/motor/components/AccountMenu.svelte';
	import { games } from '@drbrain/ui/catalog';
	import Header from '@drbrain/ui/components/Header.svelte';
	import Footer from '@drbrain/ui/components/Footer.svelte';
	import Toast from '@drbrain/ui/components/Toast.svelte';

	let { children } = $props();
	const current = $derived(games.find((game) => game.id === $page.url.pathname.split('/')[1]));
	const notice = $derived(signinNotice($page.url.searchParams.get('signin')));
	const generalDescription = 'Five browser games for spatial reasoning, programming, route planning, simulation and music.';
	const descriptions: Record<string, string> = {
		'/settings': 'Manage your drbrain profile, password and privacy.',
		'/signup': 'Create one drbrain account for all five games.',
		'/signin': 'Sign in to your drbrain account.',
		'/u/[handle]': 'See this drbrain player’s records and recent runs.'
	};
	const gamePages: Record<string, (name: string) => string> = {
		admin: (name) => `Manage custom maps for ${name}.`,
		editor: (name) => `Create and edit custom maps for ${name}.`,
		leaderboard: (name) => `Browse the best runs and compare scores in ${name}.`,
		learn: (name) => `Learn the rules, controls and agent tools for ${name}.`,
		lobby: (name) => `Find a playable or live session in ${name}.`,
		map: (name) => `Preview a map and start a run in ${name}.`,
		maps: (name) => `Find an official or community map to play in ${name}.`,
		multi: (name) => `Watch multiple live ${name} sessions together.`,
		play: (name) => `Continue a live run of ${name}.`,
		privacy: (name) => `Read the ${name} privacy policy.`,
		runs: (name) => `See recorded runs and personal bests in ${name}.`,
		sessions: (name) => `Browse active and recent ${name} sessions.`,
		watch: (name) => `Open and follow a live session in ${name}.`
	};
	const description = $derived.by(() => {
		if ($page.status >= 400) return 'Find your way back to the drbrain game collection.';
		if (current) {
			const section = $page.url.pathname.split('/')[2];
			return gamePages[section]?.(current.name) ?? current.description;
		}
		return descriptions[$page.route.id ?? ''] ?? generalDescription;
	});
	$effect(() => {
		document.querySelector('meta[name="description"]')?.setAttribute('content', description);
	});

	/** Redeems an OAuth callback's one-time code, then loads the account. */
	async function bootstrap(client: ReturnType<typeof makeClient>) {
		const url = new URL(window.location.href);
		const code = url.searchParams.get('signin_code');
		if (code) {
			await redeemSigninCode(code);
			url.searchParams.delete('signin_code');
			history.replaceState(history.state, '', url.pathname + url.search + url.hash);
		}
		await refreshAccount(client);
	}

	if (browser) {
		// Motor and the account screens share this client. Other games keep their namespace clients.
		const client = makeClient();
		setContextClient(client);
		void bootstrap(client);
	}

	function dismiss() {
		const url = $page.url;
		void goto(withoutSigninParam(url.pathname, url.search) + url.hash, { replaceState: true, noScroll: true, keepFocus: true });
	}
</script>

<div class="flex min-h-dvh flex-col">
	<a href="#main" class="suite-skip db-button">Skip to content</a>
	<Header {current}>
		{#snippet account()}<AccountMenu fullPage />{/snippet}
	</Header>
	{#if notice}<Toast {...notice} onclose={dismiss} />{/if}
	<main id="main" class="suite-main" tabindex="-1">
		{@render children()}
	</main>
	<Footer {current} />
</div>
