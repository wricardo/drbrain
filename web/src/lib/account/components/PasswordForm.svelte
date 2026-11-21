<script lang="ts">
	import { getContextClient } from '@urql/svelte';
	import { handleProblem, passwordProblem, safeNext, submitPassword, type PasswordMode } from '$lib/account/account';
	import Button from '@drbrain/ui/components/Button.svelte';
	import Input from '@drbrain/ui/components/Input.svelte';

	let {
		mode = 'login',
		next,
		onsuccess = (to: string) => window.location.assign(to)
	}: { mode?: PasswordMode; next: string; onsuccess?: (to: string) => void } = $props();

	const id = $props.id();
	const client = getContextClient();
	let handle = $state('');
	let password = $state('');
	let show = $state(false);
	let busy = $state(false);
	let attempted = $state(false);
	let touchedHandle = $state(false);
	let touchedPassword = $state(false);
	let error = $state('');

	const registering = $derived(mode === 'register');
	const handleError = $derived(registering ? handleProblem(handle) : handle.trim() ? null : 'Enter your username.');
	const passwordError = $derived(registering ? passwordProblem(password, handle) : password ? null : 'Enter your password.');
	const handleHint = $derived(attempted || touchedHandle ? handleError : null);
	const passwordHint = $derived(attempted || touchedPassword ? passwordError : null);

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		attempted = true;
		error = '';
		if (busy) return;
		if (handleError || passwordError) {
			const form = e.currentTarget as HTMLFormElement;
			const invalid = form.elements.namedItem(handleError ? 'username' : 'password');
			if (invalid instanceof HTMLInputElement) invalid.focus();
			return;
		}
		busy = true;
		const result = await submitPassword(mode, handle, password, client);
		if (result.ok) {
			onsuccess(safeNext(next));
		} else {
			busy = false;
			error = result.message;
		}
	}
</script>

<form onsubmit={submit} novalidate class="password-form" data-testid="password-form" aria-label="Username and password" aria-busy={busy}>
	<div class="field">
		<label for="{id}-handle">Username</label>
		<Input
			id="{id}-handle"
			name="username"
			bind:value={handle}
			maxlength={21}
			autocomplete="username"
			autocapitalize="none"
			spellcheck={false}
			required
			readonly={busy}
			aria-invalid={!!handleHint}
			aria-describedby={registering || handleHint ? `${id}-handle-help` : undefined}
			onblur={() => (touchedHandle = true)}
			oninput={() => (error = '')}
			data-testid="pw-handle"
		/>
		{#if handleHint}
			<p id="{id}-handle-help" class="field-error" role="alert">{handleHint}</p>
		{:else if registering}
			<p id="{id}-handle-help" class="hint">Your public handle. Use 3–20 letters, numbers or underscores.</p>
		{/if}
	</div>

	<div class="field">
		<label for="{id}-password">Password</label>
		<div class="secret">
			<Input
				id="{id}-password"
				name="password"
				type={show ? 'text' : 'password'}
				bind:value={password}
				autocomplete={registering ? 'new-password' : 'current-password'}
				required
				readonly={busy}
				aria-invalid={!!passwordHint}
				aria-describedby={registering || passwordHint ? `${id}-password-help` : undefined}
				onblur={() => (touchedPassword = true)}
				oninput={() => (error = '')}
				data-testid="pw-password"
			/>
			<button type="button" aria-label={show ? 'Hide password' : 'Show password'} aria-controls="{id}-password" onclick={() => (show = !show)}>
				{show ? 'Hide' : 'Show'}
			</button>
		</div>
		{#if passwordHint}
			<p id="{id}-password-help" class="field-error" role="alert">{passwordHint}</p>
		{:else if registering}
			<p id="{id}-password-help" class="hint">Use at least 8 characters.</p>
		{/if}
	</div>

	{#if registering}
		<p class="hint" data-testid="pw-warning">Keep your password somewhere safe. Password accounts have no email for recovery.</p>
	{/if}
	{#if error}<p class="request-error" role="alert" data-testid="pw-error">{error}</p>{/if}
	<Button type="submit" disabled={busy} data-testid="pw-submit" class="w-full">
		{busy ? registering ? 'Creating your account…' : 'Signing in…' : registering ? 'Create account' : 'Sign in'}
	</Button>
</form>

<style>
	.password-form { display: grid; gap: .95rem; }
	.field { display: grid; gap: .36rem; }
	label { font-size: .85rem; font-weight: 680; }
	.secret { position: relative; }
	.secret :global(input) { padding-inline-end: 4.2rem; }
	.secret button { position: absolute; inset: 1px 1px 1px auto; min-width: 60px; border-radius: 9px; color: var(--db-brand); font-size: .82rem; font-weight: 680; cursor: pointer; }
	.secret button:hover { background: var(--db-tint); }
	.hint, .field-error, .request-error { color: var(--db-muted); font-size: .8rem; line-height: 1.45; }
	.field-error, .request-error { color: var(--db-danger); }
	.request-error { padding: .7rem .85rem; background: var(--db-tint); border-radius: 8px; }
</style>
