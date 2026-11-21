<script lang="ts">
	import { safeNext } from '$lib/account/account';
	import type { AuthProvider } from '$lib/account/types';
	import Modal from '@drbrain/ui/components/Modal.svelte';
	import AuthOptions from './AuthOptions.svelte';

	let { providers, next, onclose }: { providers: AuthProvider[]; next: string; onclose: () => void } = $props();
</script>

<Modal title="Sign in" {onclose}>
	<div class="space-y-4 text-sm" data-testid="signin-dialog">
		<p>You can keep playing as a guest. Sign in to keep your Motor Programming runs on your profile.</p>
		<AuthOptions {providers} {next} />
		<p>New to drbrain? <a class="db-text-link" href="/signup?next={encodeURIComponent(safeNext(next))}">Sign up</a></p>
		<p class="privacy">We keep your handle and display name, never your email. Manage profile visibility in Settings.</p>
	</div>
</Modal>

<style>
	.privacy { color: var(--db-muted); font-size: .8rem; }
</style>
