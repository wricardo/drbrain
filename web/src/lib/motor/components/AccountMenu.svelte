<script lang="ts">
	import { page } from '$app/stores';
	import { getContextClient } from '@urql/svelte';
	import { profileHref } from '$lib/account/account';
	import { account, signOut } from '$lib/account/stores/account';
	import SignInDialog from '$lib/account/components/SignInDialog.svelte';

	/**
	 * Header control. Hidden while loading and when the server offers no way to sign in (the
	 * feature is off). Signed out: a "Sign in" button that opens the provider chooser. Signed in:
	 * the handle with a small menu (My profile, My runs, Settings, Sign out).
	 */
	const client = getContextClient();
	let open = $state(false);
	let chooser = $state(false);
	let signingOut = $state(false);
	let root: HTMLDivElement | undefined = $state();

	const next = $derived($page.url.pathname + $page.url.search);

	async function logout() {
		signingOut = true;
		try {
			await signOut(client);
		} catch {
			// The local state is cleared anyway; the cookie may linger until it expires.
		} finally {
			signingOut = false;
			open = false;
		}
	}

	const item = 'block w-full rounded-lg px-3 py-1.5 text-left text-sm text-indigo-900 hover:bg-indigo-100';
</script>

<svelte:window
	onkeydown={(e) => {
		if (e.key === 'Escape') open = false;
	}}
	onclick={(e) => {
		if (open && root && !root.contains(e.target as Node)) open = false;
	}}
/>

{#if $account.loaded}
	{#if $account.me}
		<div class="relative" bind:this={root} data-testid="account-menu">
			<button
				type="button"
				aria-haspopup="menu"
				aria-expanded={open}
				onclick={() => (open = !open)}
				class="flex max-w-[11rem] items-center gap-1 rounded-full border border-indigo-200 px-3 py-1 text-sm font-medium text-indigo-900 hover:bg-indigo-100"
				data-testid="account-button"
			>
				<span class="truncate">{$account.me.handle}</span>
				<span aria-hidden="true" class="text-xs">▾</span>
			</button>
			{#if open}
				<div role="menu" aria-label="Account" class="absolute right-0 z-40 mt-1 w-48 rounded-xl border border-indigo-200 bg-white p-1 shadow-lg">
					<a role="menuitem" href={profileHref($account.me.handle)} class={item} onclick={() => (open = false)}>My profile</a>
					<a role="menuitem" href="/motor/runs" class={item} onclick={() => (open = false)}>My runs</a>
					<a role="menuitem" href="/settings" class={item} onclick={() => (open = false)}>Settings</a>
					<button role="menuitem" type="button" class={item} disabled={signingOut} onclick={logout} data-testid="signout">Sign out</button>
				</div>
			{/if}
		</div>
	{:else if $account.providers.length > 0}
		<button
			type="button"
			onclick={() => (chooser = true)}
			class="rounded-full border border-indigo-300 px-3 py-1 text-sm font-medium text-indigo-900 hover:bg-indigo-100"
			data-testid="signin-button">Sign in</button
		>
	{/if}
{/if}

{#if chooser}
	<SignInDialog providers={$account.providers} {next} onclose={() => (chooser = false)} />
{/if}
