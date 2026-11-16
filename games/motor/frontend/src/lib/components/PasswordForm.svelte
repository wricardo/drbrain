<script lang="ts">
	import { getContextClient } from '@urql/svelte';
	import { normalizeHandle, handleProblem, passwordProblem, safeNext, submitPassword, type PasswordMode } from '$lib/account';

	/**
	 * Username and password sign-in / sign-up. The username is the public handle. There is no email,
	 * so there is no password reset, and the form says so before an account is created. On success the
	 * server has set the login cookie and the page is reloaded at `next` so everything sees the player.
	 */
	let {
		next,
		onsuccess = (to: string) => window.location.assign(to)
	}: { next: string; onsuccess?: (to: string) => void } = $props();

	const client = getContextClient();

	let mode = $state<PasswordMode>('login');
	let handle = $state('');
	let password = $state('');
	let show = $state(false);
	let busy = $state(false);
	let error = $state('');

	const registering = $derived(mode === 'register');
	const handleHint = $derived(registering && handle ? handleProblem(handle) : null);
	const passwordHint = $derived(registering && password ? passwordProblem(password, handle) : null);
	const ready = $derived(
		!busy && handle.trim() !== '' && password !== '' && (!registering || (!handleProblem(handle) && !passwordProblem(password, handle)))
	);

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		if (!ready) return;
		busy = true;
		error = '';
		const result = await submitPassword(mode, handle, password, client);
		busy = false;
		if (result.ok) {
			password = '';
			onsuccess(safeNext(next));
		} else {
			error = result.message;
		}
	}

	function choose(m: PasswordMode) {
		mode = m;
		error = '';
	}

	const field = 'w-full rounded-lg border border-indigo-300 bg-white px-3 py-1.5 text-sm';
	const tab = 'flex-1 rounded-full px-3 py-1 text-sm font-medium';
</script>

<form onsubmit={submit} class="space-y-3" data-testid="password-form" aria-label="Username and password">
	<div class="flex gap-1 rounded-full border border-indigo-200 p-0.5" role="group" aria-label="Sign in or create an account">
		<button
			type="button"
			aria-pressed={!registering}
			class="{tab} {!registering ? 'bg-indigo-600 text-white' : 'text-indigo-900 hover:bg-indigo-100'}"
			onclick={() => choose('login')}
			data-testid="pw-tab-login">Sign in</button
		>
		<button
			type="button"
			aria-pressed={registering}
			class="{tab} {registering ? 'bg-indigo-600 text-white' : 'text-indigo-900 hover:bg-indigo-100'}"
			onclick={() => choose('register')}
			data-testid="pw-tab-register">Create account</button
		>
	</div>

	<div>
		<label for="pw-handle" class="block text-sm font-medium text-indigo-950">Username</label>
		<input
			id="pw-handle"
			class={field}
			bind:value={handle}
			maxlength="21"
			autocomplete="username"
			autocapitalize="none"
			spellcheck="false"
			data-testid="pw-handle"
		/>
		{#if registering}
			<p class="mt-1 text-xs text-slate-600">
				This is your public handle, shown on the leaderboard: 3-20 lower-case letters, digits or underscores. Don't use your real name.
			</p>
			{#if handleHint}<p class="mt-1 text-xs text-rose-700" role="alert">{handleHint}</p>{/if}
		{/if}
	</div>

	<div>
		<label for="pw-password" class="block text-sm font-medium text-indigo-950">Password</label>
		<input
			id="pw-password"
			class={field}
			type={show ? 'text' : 'password'}
			bind:value={password}
			autocomplete={registering ? 'new-password' : 'current-password'}
			data-testid="pw-password"
		/>
		<label class="mt-1 flex items-center gap-2 text-xs text-slate-600">
			<input type="checkbox" bind:checked={show} /> Show password
		</label>
		{#if registering}
			<p class="mt-1 text-xs text-slate-600">At least 8 characters; a few random words make a good one.</p>
			{#if passwordHint}<p class="mt-1 text-xs text-rose-700" role="alert">{passwordHint}</p>{/if}
		{/if}
	</div>

	{#if registering}
		<p class="rounded-lg border border-amber-300 bg-amber-50 px-3 py-2 text-xs text-amber-900" data-testid="pw-warning">
			<strong>No email, no reset.</strong> We don't keep an email address, so if you forget this password nobody can get the account back for
			you. Pick one you will remember, or write it down.
		</p>
	{/if}

	{#if error}<p class="text-sm text-rose-700" role="alert" data-testid="pw-error">{error}</p>{/if}

	<button
		type="submit"
		class="w-full rounded-full bg-indigo-600 px-4 py-2 text-sm font-semibold text-white hover:bg-indigo-700 disabled:opacity-60"
		disabled={!ready}
		data-testid="pw-submit">{busy ? 'Please wait…' : registering ? 'Create account' : 'Sign in'}</button
	>
</form>
