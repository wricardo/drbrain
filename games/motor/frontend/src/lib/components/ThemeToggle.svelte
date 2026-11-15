<script lang="ts">
	import { themeChoice, setThemeChoice } from '$lib/stores/theme';
	import type { ThemeChoice } from '$lib/theme';

	const options: { value: ThemeChoice; label: string }[] = [
		{ value: 'system', label: 'System' },
		{ value: 'light', label: 'Light' },
		{ value: 'dark', label: 'Dark' }
	];
</script>

<!-- A three-way choice: follow the OS (default), or force light or dark. -->
<div role="radiogroup" aria-label="Colour theme" class="inline-flex rounded-full border border-indigo-200 p-0.5" data-testid="theme-toggle">
	{#each options as opt (opt.value)}
		{@const selected = $themeChoice === opt.value}
		<button
			type="button"
			role="radio"
			aria-checked={selected}
			aria-label={opt.label}
			title="{opt.label} theme"
			onclick={() => setThemeChoice(opt.value)}
			class="flex h-7 w-7 items-center justify-center rounded-full transition-colors {selected
				? 'bg-indigo-600 text-white'
				: 'text-indigo-900 hover:bg-indigo-100'}"
		>
			<svg viewBox="0 0 24 24" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
				{#if opt.value === 'system'}
					<rect x="3" y="4" width="18" height="12" rx="2" />
					<path d="M8 20h8M12 16v4" />
				{:else if opt.value === 'light'}
					<circle cx="12" cy="12" r="4" />
					<path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4" />
				{:else}
					<path d="M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8z" />
				{/if}
			</svg>
		</button>
	{/each}
</div>
