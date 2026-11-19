<script lang="ts">
	import { getContextClient } from '@urql/svelte';
	import { unwrap } from '$lib/api';
	import { claimOffer, claimResultText, summarizeClaims } from '$lib/account';
	import { describeError } from '$lib/errors';
	import { CLAIM_SESSIONS_MUTATION } from '$lib/queries';
	import { claimableSessions, forgetOwnerTokens } from '$lib/sessions';
	import { account } from '$lib/stores/account';
	import type { SessionClaimResult } from '$lib/types';

	/**
	 * After signing in: offers to attach this browser's guest runs to the account. Never
	 * automatic, because a shared computer may hold someone else's guest runs. Each run needs
	 * the secret owner token this browser kept when it created the session; a session id alone
	 * is public and cannot be claimed.
	 */
	const client = getContextClient();
	const DISMISSED_KEY = 'drbrain-motor-programming.claimDismissed';

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
			const data = await unwrap<{ claimSessions: SessionClaimResult[] }>(client.mutation(CLAIM_SESSIONS_MUTATION, { claims: claimable }));
			const outcome = summarizeClaims(data.claimSessions);
			forgetOwnerTokens(outcome.handled);
			claimable = claimableSessions();
			message = claimResultText(outcome);
		} catch (e) {
			message = describeError(e).message || 'Could not add your runs. Try again from Settings.';
		} finally {
			busy = false;
		}
	}
</script>

{#if $account.me && !dismissed && (claimable.length > 0 || message)}
	<div class="border-b border-indigo-200 bg-indigo-50 px-4 py-2 text-sm text-indigo-950 sm:px-6" role="region" aria-label="Guest runs" data-testid="claim-banner">
		<div class="mx-auto flex max-w-6xl flex-wrap items-center gap-x-3 gap-y-2">
			<p class="flex-1" role="status">{message || claimOffer(claimable.length)}</p>
			{#if claimable.length > 0 && !message}
				<button
					type="button"
					class="rounded-full bg-indigo-600 px-4 py-1 text-sm font-semibold text-white hover:bg-indigo-700 disabled:opacity-60"
					disabled={busy}
					onclick={claim}
					data-testid="claim-button">{busy ? 'Adding…' : 'Add them'}</button
				>
			{/if}
			<button type="button" class="rounded-full border border-indigo-300 bg-white px-3 py-1 text-sm hover:bg-indigo-100" onclick={dismiss}
				>{message ? 'Close' : 'Not now'}</button
			>
		</div>
	</div>
{/if}
