<script lang="ts">
	import { page } from '$app/stores';
	import { safeNext, type PasswordMode } from '$lib/account/account';
	import { account } from '$lib/account/stores/account';
	import AuthOptions from './AuthOptions.svelte';
	import Button from '@drbrain/ui/components/Button.svelte';
	import Skeleton from '@drbrain/ui/components/Skeleton.svelte';

	let { mode }: { mode: PasswordMode } = $props();
	const next = $derived(safeNext($page.url.searchParams.get('next')));
	const register = $derived(mode === 'register');
	const other = $derived((register ? '/signin' : '/signup') + '?next=' + encodeURIComponent(next));
</script>

<div class="db-container auth-container">
	<section class="db-panel auth">
		<div class="content">
			<h1>{register ? 'Create your account' : 'Welcome back'}</h1>
			<p class="intro">{register ? 'One account for all five drbrain games.' : 'Sign in to pick up where you left off.'}</p>
			{#if !$account.loaded}
				<Skeleton label="Loading sign-in options…" height="12rem" />
			{:else if $account.me}
				<p class="hint">You’re signed in as <strong>@{$account.me.handle}</strong>.</p>
				<Button href={next}>Continue <span aria-hidden="true">→</span></Button>
			{:else if $account.providers.length === 0}
				<p class="hint">Sign-in options are unavailable. You can still play as a guest.</p>
				<Button href="/#games">Explore the games</Button>
			{:else}
				<AuthOptions providers={$account.providers} {next} {mode} />
				<p class="hint">
					{register ? 'Already have an account?' : 'New to drbrain?'}
					<a class="db-text-link" href={other}>{register ? 'Sign in' : 'Sign up'}</a>
				</p>
			{/if}
		</div>
		<div class="visual" aria-hidden="true">
			<img src="/art/hero.webp" alt="" width="1080" height="720" />
		</div>
	</section>
</div>

<style>
	.auth-container { padding-block: clamp(1.25rem, 6vw, 5rem); max-width: 1100px; }
	.auth { display: grid; grid-template-columns: 1fr 42%; border-radius: var(--db-radius-lg); overflow: hidden; }
	.content { padding: clamp(1.35rem, 4vw, 3.4rem); }
	h1 { font-size: clamp(1.95rem, 3.6vw, 3rem); font-weight: 710; line-height: 1.08; letter-spacing: -.055em; }
	.intro, .hint { color: var(--db-muted); font-size: .94rem; line-height: 1.5; }
	.intro { margin: .6rem 0 1.5rem; }
	.hint { margin: 1rem 0 .65rem; }
	.hint a { font-weight: 680; }
	.visual { display: grid; align-items: center; background: #342079; }
	.visual img { height: 100%; width: 100%; object-fit: cover; }
	@media (max-width: 760px) {
		.auth { grid-template-columns: 1fr; }
		.visual { display: none; }
	}
</style>
