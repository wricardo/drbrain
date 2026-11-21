<script lang="ts">
	import { page } from '$app/stores';
	import { getContextClient } from '@urql/svelte';
	import { tick } from 'svelte';
	import { profileHref, withoutSigninParam, initials } from '$lib/account/account';
	import { account, signOut } from '$lib/account/stores/account';
	import Button from '@drbrain/ui/components/Button.svelte';
	import SignInDialog from '$lib/account/components/SignInDialog.svelte';

	/**
	 * Shared account control. The suite header links to the sign-in page.
	 * Embedded game screens can open the same providers in a dialog.
	 */
	let { fullPage = false }: { fullPage?: boolean } = $props();
	const client = getContextClient();
	let open = $state(false);
	let chooser = $state(false);
	let signingOut = $state(false);
	let root: HTMLDivElement | undefined = $state();
	let trigger: HTMLButtonElement | undefined = $state();

	const next = $derived(withoutSigninParam($page.url.pathname, $page.url.search) + $page.url.hash);
	const signInPage = $derived('/signin?next=' + encodeURIComponent(next));

	async function showMenu(last = false) {
		open = true;
		await tick();
		const options = root?.querySelectorAll<HTMLElement>('[role="menuitem"]');
		if (options?.length) options[last ? options.length - 1 : 0].focus();
	}

	function onMenuKey(event: KeyboardEvent) {
		if (!open || !root) return;
		const items = [...root.querySelectorAll<HTMLElement>('[role="menuitem"]')];
		const index = items.indexOf(document.activeElement as HTMLElement);
		const next = {
			ArrowDown: (index + 1) % items.length,
			ArrowUp: (index + items.length - 1) % items.length,
			Home: 0,
			End: items.length - 1
		}[event.key];
		if (next === undefined) return;
		event.preventDefault();
		items[next]?.focus();
	}

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

</script>

<svelte:window
	onkeydown={(e) => {
		if (open && e.key === 'Escape') {
			open = false;
			trigger?.focus();
		}
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
				bind:this={trigger}
				aria-haspopup="menu"
				aria-expanded={open}
				onclick={() => { if (open) open = false; else void showMenu(); }}
				onkeydown={(event) => {
					if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
						event.preventDefault();
						void showMenu(event.key === 'ArrowUp');
					}
				}}
				class="account-trigger" aria-label="Account for @{$account.me.handle}"
				data-testid="account-button"
			>
				<span class="db-avatar size-7 text-[.68rem]" aria-hidden="true">{initials($account.me.displayName, $account.me.handle)}</span>
				<span class="truncate handle">{$account.me.handle}</span>
				<span aria-hidden="true" class="text-xs">▾</span>
			</button>
			{#if open}
				<div role="menu" tabindex="-1" aria-label="Account" onkeydown={onMenuKey} class="db-panel account-dropdown">
					<a role="menuitem" tabindex="-1" href={profileHref($account.me.handle)} class="account-item" onclick={() => (open = false)}>My profile</a>
					<a role="menuitem" tabindex="-1" href="/motor/runs" class="account-item" onclick={() => (open = false)}>My runs</a>
					<a role="menuitem" tabindex="-1" href="/settings" class="account-item" onclick={() => (open = false)}>Settings</a>
					<button role="menuitem" tabindex="-1" type="button" class="account-item" disabled={signingOut} onclick={logout} data-testid="signout">Sign out</button>
				</div>
			{/if}
		</div>
	{:else if $account.providers.length > 0}
		{#if fullPage}
			{#if !['/signin', '/signup'].includes($page.url.pathname)}
				<Button href={signInPage} variant="secondary" data-testid="signin-button">Sign in</Button>
			{/if}
		{:else}
			<Button variant="secondary" onclick={() => (chooser = true)} data-testid="signin-button">Sign in</Button>
		{/if}
	{/if}
{/if}

{#if chooser}
	<SignInDialog providers={$account.providers} {next} onclose={() => (chooser = false)} />
{/if}


<style>
	.account-trigger { display: flex; align-items: center; gap: .45rem; min-height: 44px; max-width: 170px; padding-inline: .45rem; border: 1px solid var(--db-control); border-radius: 10px; color: var(--db-ink); font-size: .875rem; font-weight: 660; cursor: pointer; }
	.account-trigger:hover, .account-trigger[aria-expanded='true'] { background: var(--db-tint); }
	.account-dropdown { position: absolute; z-index: 40; top: calc(100% + .6rem); right: 0; width: 198px; padding: .4rem; box-shadow: var(--db-shadow-raised); }
	.account-item { display: flex; align-items: center; width: 100%; min-height: 44px; padding-inline: .65rem; border-radius: 8px; color: var(--db-ink); text-align: left; font-size: .875rem; cursor: pointer; }
	.account-item:hover, .account-item:focus-visible { background: var(--db-tint); }
	@media (max-width: 580px) { .handle { max-width: 65px; } }
	@media (max-width: 420px) { .handle { position: absolute; clip-path: inset(50%); } }
</style>
