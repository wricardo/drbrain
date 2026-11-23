<script lang="ts">
	let { kind }: { kind: string } = $props();
</script>

<svg viewBox="0 0 16 16" fill="none" aria-hidden="true" class="h-full w-full">
	{#if kind === 'K'}
		<!-- Storefront and awning -->
		<path d="M3 7h10v7H3z" fill="currentColor" />
		<path d="M2 6 3.5 2h9L14 6v1H2z" fill="currentColor" />
		<path d="M5 10h3v4H5zm4-1h2v2H9z" fill="var(--color-kitchen)" />
		<path d="M4 3h8" stroke="var(--color-kitchen)" stroke-width=".8" />
	{:else if kind === 'H' || kind === 'h'}
		<path d="M1.5 7.5 8 2l6.5 5.5h-1.6V14H3.1V7.5z" fill="currentColor" />
		<path d="M7 10h2v4H7z" fill="var(--color-customer)" />
	{:else if kind === 'D'}
		<path d="M2 5 8 2l6 3v9H2z" fill="currentColor" />
		<path d="M4.5 8h7v6h-7zM5 5.5h6" stroke="var(--color-depot)" stroke-width="1.2" />
		<path d="M5.5 10h5M5.5 12h5" stroke="var(--color-depot)" stroke-width=".8" />
	{:else if kind === 'C'}
		<path d="M3 2h7v12H3zM10 5h1.5l1 1.5v5h1" fill="currentColor" stroke="currentColor" stroke-linejoin="round" />
		<path d="m7.4 3.2-2 4h1.5l-.5 3.4 2.5-4H7.4z" fill="var(--color-charger)" />
	{:else if kind === 'scooter'}
		<circle cx="5" cy="12" r="1.8" fill="currentColor" /><circle cx="11.5" cy="12" r="1.8" fill="currentColor" />
		<path d="M4 9h5l1-4h2l-1.4 5H5zM8 3h3" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" />
	{:else}
		<!-- Top-down van or truck -->
		<rect x="3" y="1" width="10" height="14" rx="2" fill="currentColor" />
		<path d="M4.5 4h7v2h-7z" fill="var(--color-ink)" />
		{#if kind === 'truck'}<path d="M4.5 8h7v5h-7z" fill="var(--color-ink)" opacity=".55" />{/if}
	{/if}
</svg>
