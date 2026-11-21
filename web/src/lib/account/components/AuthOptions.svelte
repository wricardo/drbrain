<script lang="ts">
	import { PASSWORD_PROVIDER, signInHref, type PasswordMode } from '$lib/account/account';
	import type { AuthProvider } from '$lib/account/types';
	import Button from '@drbrain/ui/components/Button.svelte';
	import PasswordForm from './PasswordForm.svelte';

	let { providers, next, mode = 'login' }: { providers: AuthProvider[]; next: string; mode?: PasswordMode } = $props();
	const password = $derived(providers.some((provider) => provider.id === PASSWORD_PROVIDER));
	const external = $derived(providers.filter((provider) => provider.id !== PASSWORD_PROVIDER));
	let redirecting = $state('');
</script>

<svelte:window onpageshow={() => (redirecting = '')} />

{#if password}<PasswordForm {mode} {next} />{/if}

{#if external.length}
	{#if password}<p class="or">or continue with</p>{/if}
	<ul class="providers" data-testid="signin-providers">
		{#each external as provider (provider.id)}
			<li>
				<Button
					variant="secondary"
					class="w-full"
					href={signInHref(provider.id, next)}
					data-sveltekit-reload
					data-testid="signin-{provider.id}"
					onclick={() => (redirecting = provider.id)}
				>
					{#if provider.id === 'google'}
						<svg aria-hidden="true" width="18" height="18" viewBox="0 0 48 48">
							<path fill="#4285f4" d="M43.6 24.5c0-1.4-.1-2.8-.4-4.1H24v7.8h11a9.3 9.3 0 0 1-4.1 6.1v5h6.7c3.9-3.6 6-8.8 6-14.8z"/>
							<path fill="#34a853" d="M24 44c5.5 0 10.1-1.8 13.6-4.7l-6.7-5c-1.8 1.2-4.1 1.9-6.9 1.9-5.3 0-9.8-3.6-11.4-8.5H5.7v5.2A20 20 0 0 0 24 44z"/>
							<path fill="#fbbc05" d="M12.6 27.7a12 12 0 0 1 0-7.4v-5.2H5.7a20 20 0 0 0 0 17.8z"/>
							<path fill="#ea4335" d="M24 11.8c3 0 5.7 1 7.8 3l5.8-5.8A19.2 19.2 0 0 0 24 4 20 20 0 0 0 5.7 15.1l6.9 5.2c1.6-4.9 6.1-8.5 11.4-8.5z"/>
						</svg>
					{:else if provider.id === 'github'}
						<svg aria-hidden="true" width="18" height="18" viewBox="0 0 24 24" fill="currentColor">
							<path d="M12 .7a11.3 11.3 0 0 0-3.6 22c.6.1.8-.2.8-.5v-2.1c-3.2.7-3.9-1.4-3.9-1.4-.5-1.3-1.2-1.6-1.2-1.6-1.1-.7.1-.7.1-.7 1.1.1 1.7 1.1 1.7 1.1 1 1.8 2.6 1.3 3.2 1 .1-.8.4-1.3.7-1.6-2.6-.3-5.3-1.3-5.3-5.7 0-1.3.5-2.3 1.2-3.2-.1-.3-.5-1.5.1-3.1 0 0 1-.3 3.3 1.2a11.5 11.5 0 0 1 5.9 0c2.2-1.5 3.2-1.2 3.2-1.2.7 1.6.3 2.8.2 3.1.8.9 1.2 1.9 1.2 3.2 0 4.4-2.7 5.4-5.3 5.7.4.4.8 1.1.8 2.2v3.1c0 .3.2.6.8.5A11.3 11.3 0 0 0 12 .7Z"/>
						</svg>
					{:else}
						<span class="dev-mark" aria-hidden="true">&lt;/&gt;</span>
					{/if}
					{redirecting === provider.id ? `Opening ${provider.id === 'dev' ? 'development sign-in' : provider.name}…` : provider.id === 'dev' ? 'Use development sign-in' : `Continue with ${provider.name}`}
				</Button>
			</li>
		{/each}
	</ul>
{/if}

<style>
	.or { display: flex; align-items: center; gap: .65rem; margin: 1rem 0; color: var(--db-muted); font-size: .79rem; }
	.or::before, .or::after { content: ''; height: 1px; background: var(--db-line); flex: 1; }
	.providers { display: grid; gap: .55rem; }
	.dev-mark { font-family: ui-monospace, monospace; color: var(--db-brand); }
</style>
