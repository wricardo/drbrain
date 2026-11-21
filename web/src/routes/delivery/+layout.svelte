<script lang="ts">
	import GameNav from '@drbrain/ui/components/GameNav.svelte';
	import { games } from '@drbrain/ui/catalog';
	import ClaimBanner from '$lib/delivery/components/ClaimBanner.svelte';
	const game = games.find((item) => item.id === 'delivery')!;

	let { children } = $props();
</script>

<div class="suite-game tabular-nums" data-game="delivery">
	<GameNav {game} />
	<ClaimBanner />
	{@render children()}
</div>
