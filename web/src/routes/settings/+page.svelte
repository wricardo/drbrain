<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/stores';
	import { getContextClient } from '@urql/svelte';
	import { unwrap } from '@drbrain/ui/graphql';
	import {
		HANDLE_RE,
		MAX_DISPLAY_NAME,
		handleChangeText,
		handleProblem,
		initials,
		normalizeHandle,
		passwordProblem,
		profileHref
	} from '$lib/account/account';
	import { claimOffer, claimResultText, summarizeClaims } from '$lib/motor/profile';
	import { describeError } from '@drbrain/ui/errors';
	import { accountRequestContext, CHANGE_PASSWORD_MUTATION, DELETE_ACCOUNT_MUTATION, UPDATE_ACCOUNT_MUTATION } from '$lib/account/queries';
	import { clearAuthToken } from '$lib/account/token';
	import { CLAIM_SESSIONS_MUTATION } from '$lib/motor/queries';
	import { claimableSessions, forgetAllOwnerTokens, forgetOwnerTokens } from '$lib/motor/sessions';
	import { account, setMe } from '$lib/account/stores/account';
	import type { Account } from '$lib/account/types';
	import type { SessionClaimResult } from '$lib/motor/types';
	import Button from '@drbrain/ui/components/Button.svelte';
	import EmptyState from '@drbrain/ui/components/EmptyState.svelte';
	import Skeleton from '@drbrain/ui/components/Skeleton.svelte';

	const client = getContextClient();
	const me = $derived($account.me);

	let handle = $state('');
	let displayName = $state('');
	let isPublic = $state(true);
	let seeded = $state('');
	let saving = $state(false);
	let saved = $state('');
	let problem = $state('');

	// Fill the form once per signed-in handle (not after every save, which would clobber edits).
	$effect(() => {
		if (me && seeded !== me.handle) {
			handle = me.handle;
			displayName = me.displayName;
			isPublic = me.profilePublic;
			seeded = me.handle;
		}
	});

	const handleChanged = $derived(me ? normalizeHandle(handle) !== me.handle : false);
	const wait = $derived(me ? handleChangeText(me) : null);
	const dirty = $derived(!!me && (handleChanged || displayName.trim() !== me.displayName || isPublic !== me.profilePublic));
	const liveProblem = $derived(handleChanged ? handleProblem(handle) : null);

	async function save(e: SubmitEvent) {
		e.preventDefault();
		if (!me || saving || !dirty || liveProblem) return;
		saving = true;
		problem = '';
		saved = '';
		const input: Record<string, unknown> = {};
		if (handleChanged) input.handle = normalizeHandle(handle);
		if (displayName.trim() !== me.displayName) input.displayName = displayName.trim();
		if (isPublic !== me.profilePublic) input.profilePublic = isPublic;
		try {
			const data = await unwrap<{ account: { updateAccount: Account } }>(client.mutation(UPDATE_ACCOUNT_MUTATION, { input }));
			setMe(data.account.updateAccount);
			handle = data.account.updateAccount.handle;
			displayName = data.account.updateAccount.displayName;
			isPublic = data.account.updateAccount.profilePublic;
			seeded = data.account.updateAccount.handle;
			saved = 'Saved.';
		} catch (err) {
			problem = describeError(err).message || 'Could not save.';
		} finally {
			saving = false;
		}
	}

	// ---- password (username/password accounts only) ----
	let currentPw = $state('');
	let newPw = $state('');
	let pwBusy = $state(false);
	let pwMessage = $state('');
	let pwError = $state('');
	const newPwProblem = $derived(newPw ? passwordProblem(newPw, me?.handle ?? '') : null);

	async function changePassword(e: SubmitEvent) {
		e.preventDefault();
		if (pwBusy || !currentPw || !newPw || newPwProblem) return;
		pwBusy = true;
		pwMessage = '';
		pwError = '';
		try {
			await unwrap(client.mutation(CHANGE_PASSWORD_MUTATION, { currentPassword: currentPw, newPassword: newPw }));
			currentPw = '';
			newPw = '';
			pwMessage = 'Password changed. Your other browsers were signed out.';
		} catch (err) {
			pwError = describeError(err).message || 'Could not change the password.';
		} finally {
			pwBusy = false;
		}
	}

	// ---- guest runs of this browser ----
	let claimable = $state(claimableSessions());
	let claiming = $state(false);
	let claimMessage = $state('');

	async function claim() {
		claiming = true;
		claimMessage = '';
		try {
			const data = await unwrap<{ claimSessions: SessionClaimResult[] }>(client.mutation(CLAIM_SESSIONS_MUTATION, { claims: claimable }));
			const outcome = summarizeClaims(data.claimSessions);
			forgetOwnerTokens(outcome.handled);
			claimable = claimableSessions();
			claimMessage = claimResultText(outcome);
		} catch (err) {
			claimMessage = describeError(err).message || 'Could not add your runs.';
		} finally {
			claiming = false;
		}
	}

	// ---- delete ----
	let confirming = $state(false);
	let typed = $state('');
	let deleting = $state(false);
	let deleteProblem = $state('');
	const canDelete = $derived(!!me && normalizeHandle(typed) === me.handle && !deleting);

	async function deleteAccount() {
		if (!me || !canDelete) return;
		deleting = true;
		deleteProblem = '';
		try {
			await unwrap(client.mutation(DELETE_ACCOUNT_MUTATION, { confirmHandle: normalizeHandle(typed) }, accountRequestContext));
			forgetAllOwnerTokens(); // the old runs must not follow a future account
			clearAuthToken();
			setMe(null);
			await goto('/motor');
		} catch (err) {
			deleteProblem = describeError(err).message || 'Could not delete the account.';
		} finally {
			deleting = false;
		}
	}

	const field = 'db-input mt-1';
</script>

<svelte:head><title>Account settings · drbrain</title></svelte:head>

<div class="db-container settings">
	<header class="settings-heading">
		{#if me}<span class="db-avatar size-14 text-xl" aria-hidden="true">{initials(me.displayName, me.handle)}</span>{/if}
		<div>
			<h1 class="account-title">Account settings</h1>
			{#if me}<p>@{me.handle} · <a href={profileHref(me.handle)} class="db-text-link">View your profile</a></p>
			{:else}<p>Manage your profile, privacy and sign-in details.</p>{/if}
		</div>
	</header>

	{#if !$account.loaded}
		<Skeleton label="Loading your account…" />
	{:else if !me}
		<div data-testid="settings-signed-out">
			<EmptyState title="Make it yours" description="Sign in to manage your drbrain profile and privacy settings.">
				{#if $account.providers.length > 0}
					<Button href="/signin?next={encodeURIComponent($page.url.pathname + $page.url.search)}">Sign in</Button>
				{:else}
					<Button href="/#games">Explore the games</Button>
				{/if}
			</EmptyState>
		</div>
	{:else}
		<div class="settings-grid">
			<form class="settings-panel db-panel space-y-4" onsubmit={save} data-testid="settings-form">
				<h2 class="text-xl font-bold tracking-tight">Profile</h2>
				<div>
					<label for="s-handle" class="block text-sm font-medium text-indigo-950">Handle</label>
					<input id="s-handle" class={field} bind:value={handle} maxlength="21" autocomplete="off" autocapitalize="none" spellcheck="false" aria-describedby="s-handle-help" data-testid="s-handle" />
					<p id="s-handle-help" class="mt-1 text-xs text-slate-600">
						3-20 lower-case letters, digits or underscores. Your profile is at
						<a href={profileHref(me.handle)} class="text-indigo-700 underline">/u/{me.handle}</a>.
						{#if wait}<span data-testid="handle-wait">{wait}</span>{/if}
					</p>
					{#if liveProblem}<p class="mt-1 text-xs text-rose-700" role="alert">{liveProblem}</p>{/if}
				</div>
				<div>
					<label for="s-name" class="block text-sm font-medium text-indigo-950">Display name</label>
					<input id="s-name" class={field} bind:value={displayName} maxlength={MAX_DISPLAY_NAME} autocomplete="off" data-testid="s-name" />
					<p class="mt-1 text-xs text-slate-600">Shown on the leaderboard and your profile. Leave it empty to use your handle.</p>
				</div>
				<fieldset>
					<legend class="text-sm font-medium text-indigo-950">Who can see your profile</legend>
					<label class="visibility">
						<input type="radio" name="vis" checked={isPublic} onchange={() => (isPublic = true)} class="mt-1" data-testid="vis-public" />
						<span><strong class="text-indigo-950">Public.</strong> Anyone can open your profile; the leaderboard shows your name and links to it.</span>
					</label>
					<label class="visibility">
						<input type="radio" name="vis" checked={!isPublic} onchange={() => (isPublic = false)} class="mt-1" data-testid="vis-private" />
						<span><strong class="text-indigo-950">Private.</strong> Your profile page is hidden and the leaderboard shows “Private player” instead of your name.</span>
					</label>
				</fieldset>
				<div class="flex flex-wrap items-center gap-3">
					<button
						type="submit"
						class="db-button"
						disabled={!dirty || saving || !!liveProblem}
						data-testid="s-save">{saving ? 'Saving…' : 'Save changes'}</button
					>
					<span class="text-sm text-emerald-700" role="status" aria-live="polite">{saved}</span>
				</div>
				{#if problem}<p class="text-sm text-rose-700" role="alert" data-testid="s-problem">{problem}</p>{/if}
			</form>

			<div class="settings-side">
				{#if me.hasPassword}
					<form class="settings-panel db-panel space-y-3" onsubmit={changePassword} data-testid="password-settings">
						<h2 class="text-xl font-bold tracking-tight">Password</h2>
						<div>
							<label for="s-cur-pw" class="block text-sm font-medium text-indigo-950">Current password</label>
							<input id="s-cur-pw" type="password" class={field} bind:value={currentPw} autocomplete="current-password" data-testid="s-cur-pw" />
						</div>
						<div>
							<label for="s-new-pw" class="block text-sm font-medium text-indigo-950">New password</label>
							<input id="s-new-pw" type="password" class={field} bind:value={newPw} autocomplete="new-password" data-testid="s-new-pw" />
							{#if newPwProblem}<p class="mt-1 text-xs text-rose-700" role="alert">{newPwProblem}</p>{/if}
						</div>
						<p class="text-xs text-slate-600">
							There is no email on this account, so a forgotten password cannot be reset. Changing it signs your other browsers out.
						</p>
						<div class="flex flex-wrap items-center gap-3">
							<button
								type="submit"
								class="db-button"
								disabled={pwBusy || !currentPw || !newPw || !!newPwProblem}
								data-testid="s-pw-save">{pwBusy ? 'Saving…' : 'Change password'}</button
							>
							<span class="text-sm text-emerald-700" role="status" aria-live="polite" data-testid="s-pw-ok">{pwMessage}</span>
						</div>
						{#if pwError}<p class="text-sm text-rose-700" role="alert" data-testid="s-pw-error">{pwError}</p>{/if}
					</form>
				{/if}

				<section class="settings-panel db-panel space-y-2" aria-labelledby="guest-title">
					<h2 id="guest-title" class="text-xl font-bold tracking-tight">Guest runs in this browser</h2>
					{#if claimable.length > 0}
						<p class="text-sm text-slate-700" data-testid="guest-offer">{claimOffer(claimable.length)}</p>
						<button
							type="button"
							class="db-button"
							disabled={claiming}
							onclick={claim}
							data-testid="s-claim">{claiming ? 'Adding…' : 'Add them to my profile'}</button
						>
					{:else}
						<p class="text-sm text-slate-700">No guest runs are waiting in this browser.</p>
					{/if}
					{#if claimMessage}<p class="text-sm text-slate-700" role="status" data-testid="claim-result">{claimMessage}</p>{/if}
					<p class="text-xs text-slate-600">Eligible Motor Programming runs from this browser can be added to your profile.</p>
				</section>

				<section class="settings-panel db-panel danger space-y-2" aria-labelledby="danger-title">
					<h2 id="danger-title" class="text-lg font-semibold text-rose-900">Delete account</h2>
					<p class="text-sm text-slate-700">
						This removes your account, your profile page and every login. Your past wins stay on the leaderboard as guest entries under a new random
						name, with nothing linking them to you. It cannot be undone.
					</p>
					{#if !confirming}
						<button
							type="button"
							class="db-button" data-variant="secondary"
							onclick={() => (confirming = true)}
							data-testid="delete-start">Delete my account…</button
						>
					{:else}
						<div class="space-y-2 rounded-lg border border-rose-300 bg-rose-50 p-3" role="group" aria-label="Confirm account deletion" data-testid="delete-confirm">
							<label for="s-confirm" class="block text-sm text-rose-900">Type your handle (<strong>{me.handle}</strong>) to confirm:</label>
							<input id="s-confirm" class={field} bind:value={typed} autocomplete="off" autocapitalize="none" spellcheck="false" pattern={HANDLE_RE.source} data-testid="delete-input" />
							<div class="flex flex-wrap gap-2">
								<button
									type="button"
									class="rounded-full bg-rose-700 px-4 py-1.5 text-sm font-semibold text-white hover:bg-rose-800 disabled:opacity-50"
									disabled={!canDelete}
									onclick={deleteAccount}
									data-testid="delete-go">{deleting ? 'Deleting…' : 'Delete forever'}</button
								>
								<button
									type="button"
									class="rounded-full border border-indigo-300 bg-white px-4 py-1.5 text-sm hover:bg-indigo-50"
									onclick={() => {
										confirming = false;
										typed = '';
										deleteProblem = '';
									}}>Cancel</button
								>
							</div>
							{#if deleteProblem}<p class="text-sm text-rose-800" role="alert">{deleteProblem}</p>{/if}
						</div>
					{/if}
				</section>
			</div>
		</div>
	{/if}
</div>


<style>
	.settings { max-width: 1100px; padding-block: clamp(1.6rem, 5vw, 4.25rem); }
	.settings-heading { display: flex; align-items: center; gap: .9rem; margin-bottom: 1.5rem; }
	.account-title { font-size: clamp(1.9rem, 4vw, 3rem); font-weight: 710; letter-spacing: -.052em; line-height: 1.1; }
	.settings-heading p { margin-top: .42rem; color: var(--db-muted); font-size: .9rem; }
	.settings-grid { display: grid; grid-template-columns: 1.2fr 1fr; align-items: start; gap: 1rem; }
	.settings-side { display: grid; gap: 1rem; }
	.settings-panel { padding: clamp(1.15rem, 3vw, 1.7rem); }
	.visibility { display: flex; align-items: flex-start; gap: .65rem; border: 1px solid var(--db-line); border-radius: 9px; padding: .65rem; margin-top: .45rem; cursor: pointer; color: var(--db-muted); font-size: .84rem; }
	.visibility:has(input:checked) { background: var(--db-tint); }
	.danger { border-color: #e6b7c1; }
	@media (max-width: 760px) { .settings-grid { grid-template-columns: 1fr; } }
</style>
