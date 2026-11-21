<script lang="ts">
	import { page } from '$app/stores';
	import Button from '@drbrain/ui/components/Button.svelte';
	import Hero from '@drbrain/ui/components/Hero.svelte';
</script>

<svelte:head><title>{$page.status === 404 ? 'Page not found' : 'Something went wrong'} · drbrain</title></svelte:head>

<div class="db-container py-8 sm:py-14">
	<Hero
		label={String($page.status)}
		title={$page.status === 404 ? 'You took a wrong turn.' : 'Something went wrong.'}
		description={$page.status === 404 ? 'This page may have moved. Find your way back to the game collection.' : 'Return to the game collection and try again.'}
		image="/art/hero.webp"
	>
		<Button href="/">Back to drbrain <span aria-hidden="true">→</span></Button>
	</Hero>
</div>
