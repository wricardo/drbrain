<script lang="ts">
	import '../app.css';
	import { browser } from '$app/environment';
	import { setContextClient } from '@urql/svelte';
	import { makeClient } from '$lib/motor/graphql';
	import { refreshAccount } from '$lib/account/stores/account';

	let { children } = $props();

	// Every game layout installs its own client; this one covers the global
	// routes (settings, profiles) and seeds the global account store.
	if (browser) {
		const client = makeClient();
		setContextClient(client);
		void refreshAccount(client);
	}
</script>

{@render children()}
