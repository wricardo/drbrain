<script lang="ts">
	import Button from '@drbrain/ui/components/Button.svelte';
	import { api } from '$lib/delivery/api';
	import { claimableSessions, forgetOwnerTokens } from '$lib/delivery/ownerTokens';
	import { account } from '$lib/account/stores/account';

	/**
	 * After signing in: offers to attach this browser's guest cities to the account. Never
	 * automatic, because a shared computer may hold someone else's guest cities. Each one needs
	 * the secret owner token this browser kept when it created the session; a session id alone
	 * is public and cannot be claimed.
	 */
	const DISMISSED_KEY = 'drbrain-delivery.claimDismissed';

	let claimable = $state(claimableSessions());
	let dismissed = $state(readDismissed());
	let busy = $state(false);
	let message = $state('');

	function readDismissed(): boolean {
		try {
			return sessionStorage.getItem(DISMISSED_KEY) === '1';
		} catch {
			return false;
		}
	}

	function dismiss() {
		dismissed = true;
		try {
			sessionStorage.setItem(DISMISSED_KEY, '1');
		} catch {
			// ignore
		}
	}

	async function claim() {
		busy = true;
		message = '';
		try {
			const results = await api.claimSessions(claimable);
			// Keep tokens the server refused: they may still be useful as play credentials.
			forgetOwnerTokens(results.filter((r) => r.status === 'ATTACHED' || r.status === 'ALREADY_YOURS').map((r) => r.sessionID));
			claimable = claimableSessions();
			const added = results.filter((r) => r.status === 'ATTACHED').length;
			message = added > 0 ? `Added ${added} ${added === 1 ? 'city' : 'cities'} to your account.` : 'Nothing new to add.';
		} catch (e) {
			message = e instanceof Error && e.message ? e.message : 'Could not add your cities. Try again later.';
		} finally {
			busy = false;
		}
	}
</script>

{#if $account.me && !dismissed && (claimable.length > 0 || message)}
	<div class="border-b border-edge bg-[var(--db-tint)] py-2 text-sm text-body" role="region" aria-label="Guest cities" data-testid="claim-banner">
		<div class="db-container flex flex-col items-stretch gap-2 sm:flex-row sm:items-center sm:gap-x-3">
			<p class="basis-full sm:flex-1" role="status">
				{message || `Add ${claimable.length} ${claimable.length === 1 ? 'city' : 'cities'} you started as a guest to your account?`}
			</p>
			<div class="flex flex-wrap gap-2">
				{#if claimable.length > 0 && !message}
					<Button disabled={busy} onclick={claim} data-testid="claim-button">{busy ? 'Adding…' : 'Add them'}</Button>
				{/if}
				<Button variant="quiet" disabled={busy} onclick={dismiss}>{message ? 'Close' : 'Not now'}</Button>
			</div>
		</div>
	</div>
{/if}
