<script lang="ts">
	import { signInHref } from '$lib/account';
	import type { AuthProvider } from '$lib/types';

	/**
	 * Small chooser of the sign-in providers this server offers. Signing in is optional:
	 * the dialog says so, and what is (and is not) kept. A provider button is a plain link
	 * the server turns into the provider's page, then back to `next`.
	 */
	let { providers, next, onclose }: { providers: AuthProvider[]; next: string; onclose: () => void } = $props();

	let closeBtn: HTMLButtonElement | undefined = $state();
	$effect(() => {
		closeBtn?.focus();
	});
</script>

<svelte:window
	onkeydown={(e) => {
		if (e.key === 'Escape') onclose();
	}}
/>

<div
	class="fixed inset-0 z-50 flex items-center justify-center bg-indigo-950/40 dark:bg-black/60 p-4"
	role="presentation"
	onclick={(e) => e.target === e.currentTarget && onclose()}
>
	<div role="dialog" aria-modal="true" aria-labelledby="signin-title" class="w-full max-w-sm rounded-2xl bg-white p-5 shadow-xl" data-testid="signin-dialog">
		<div class="flex items-center justify-between">
			<h2 id="signin-title" class="text-lg font-semibold text-indigo-950">Sign in</h2>
			<button bind:this={closeBtn} type="button" class="rounded-full px-2 text-lg leading-none text-slate-500 hover:bg-slate-100" aria-label="Close" onclick={onclose}>×</button>
		</div>
		<p class="mt-2 text-sm text-slate-700">
			Sign in to keep your runs on a profile and appear on the leaderboard under your own handle.
			<strong class="text-indigo-950">You can keep playing as a guest</strong>: nothing about the game changes if you don't.
		</p>
		<ul class="mt-4 space-y-2" data-testid="signin-providers">
			{#each providers as p (p.id)}
				<li>
					<!-- A full page navigation: the SvelteKit router must not intercept /auth routes. -->
					<a
						href={signInHref(p.id, next)}
						data-sveltekit-reload
						class="block rounded-full bg-indigo-600 px-4 py-2 text-center text-sm font-semibold text-white hover:bg-indigo-700"
						data-testid="signin-{p.id}">Continue with {p.name}</a
					>
				</li>
			{/each}
		</ul>
		<p class="mt-4 text-xs text-slate-600">
			We keep a random handle and a display name, never your email address, your photo or anything else from your account.
			Your profile is public unless you make it private in Settings.
		</p>
	</div>
</div>
