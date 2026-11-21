<script lang="ts">
	import { browser } from '$app/environment';
	import { setContextClient } from '@urql/svelte';
	import { makeClient } from '$lib/music/graphql';
	import GameNav from '@drbrain/ui/components/GameNav.svelte';
	import { games } from '@drbrain/ui/catalog';
	const game = games.find((item) => item.id === 'music')!;

	let { children } = $props();

	if (browser) setContextClient(makeClient());
</script>

<div class="suite-game" data-game="music">
	<GameNav {game} />
	{@render children()}
</div>
