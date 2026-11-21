<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { api, ApiError, type CityInfo } from '$lib/delivery/api';
	import { canEditCity, canPlayCity, requiresAdminKey, sortCities, statusLabel } from '$lib/delivery/cities';
	import { pageTitle } from '$lib/delivery/brand';
	import { account } from '$lib/account/stores/account';
	import Button from '@drbrain/ui/components/Button.svelte';
	import EmptyState from '@drbrain/ui/components/EmptyState.svelte';
	import Skeleton from '@drbrain/ui/components/Skeleton.svelte';

	let cities = $state<CityInfo[]>([]);
	let loading = $state(true);
	let failed = $state(false);
	let error = $state<string | null>(null);
	/** Id of the city whose Play button is creating a session. */
	let opening = $state<string | null>(null);

	const signedIn = $derived($account.me !== null);
	const editorHref = `/signin?next=${encodeURIComponent('/delivery/editor')}`;

	async function load() {
		loading = true;
		failed = false;
		error = null;
		try {
			cities = sortCities(await api.cities());
		} catch (e) {
			failed = true;
			error = e instanceof ApiError ? `${e.message} (${e.code})` : String(e);
		} finally {
			loading = false;
		}
	}

	async function play(city: CityInfo) {
		if (opening || !canPlayCity(city)) return;
		opening = city.id;
		error = null;
		try {
			const session = await api.createSession(undefined, undefined, city.id);
			await goto(`/delivery?city=${encodeURIComponent(session.id)}`);
		} catch (e) {
			error = e instanceof ApiError ? `${e.message} (${e.code})` : String(e);
		} finally {
			opening = null;
		}
	}

	onMount(() => {
		void load();
	});
</script>

<svelte:head>
	<title>{pageTitle('Cities')}</title>
</svelte:head>

<div class="db-container py-8">
	<header class="mb-6 flex flex-wrap items-end justify-between gap-4">
		<div>
			<p class="db-kicker">Delivery</p>
			<h1 class="text-3xl font-bold tracking-tight">Cities</h1>
			<p class="mt-1 max-w-2xl text-sm text-muted">
				Pick a published city or one of your drafts. Published cities are open to everyone.
			</p>
		</div>
		{#if signedIn}
			<Button href="/delivery/editor">New city <span aria-hidden="true">→</span></Button>
		{:else if $account.loaded}
			<Button href={editorHref} variant="secondary">Sign in to author</Button>
		{/if}
	</header>

	{#if !signedIn && $account.loaded}
		<div class="db-panel mb-6 flex flex-wrap items-center justify-between gap-3 p-4">
			<p class="text-sm text-muted">Sign in to create, edit and publish your own cities. You can still play any published city as a guest.</p>
			<Button href={editorHref} variant="secondary">Sign in</Button>
		</div>
	{/if}

	{#if error}
		<p class="mb-4 rounded-lg border border-[var(--db-danger)]/40 bg-[var(--db-danger)]/10 px-4 py-3 text-sm text-[var(--db-danger)]" role="alert">
			{error}
		</p>
	{/if}

	{#if loading}
		<div class="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
			{#each [0, 1, 2] as n (n)}
				<Skeleton label="Loading cities…" />
			{/each}
		</div>
	{:else if failed}
		<EmptyState title="Could not load cities" description="Try loading the catalogue again.">
			<Button variant="secondary" onclick={load}>Retry</Button>
		</EmptyState>
	{:else if cities.length === 0}
		<EmptyState title="No cities yet" description="Create the first community city for Delivery.">
			{#if signedIn}
				<Button href="/delivery/editor">New city</Button>
			{:else}
				<Button href={editorHref} variant="secondary">Sign in to author</Button>
			{/if}
		</EmptyState>
	{:else}
		<ul class="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
			{#each cities as city (city.id)}
				<li class="db-panel flex min-w-0 flex-col gap-4 p-4">
					<div class="min-w-0">
						<div class="flex flex-wrap items-center gap-2">
							<h2 class="truncate text-lg font-bold tracking-tight">{city.name}</h2>
							<span
								class="rounded border px-2 py-0.5 text-[11px] font-semibold {city.status ===
								'PUBLISHED'
									? 'border-[var(--db-success)] text-[var(--db-success)]'
									: 'border-edge-strong text-muted'}"
							>
								{statusLabel(city.status)}
							</span>
							{#if city.ownedByMe}
								<span class="rounded bg-[var(--db-accent)]/15 px-1.5 py-0.5 text-[11px] font-semibold text-[var(--db-accent)]">Your city</span>
							{/if}
						</div>
						<p class="mt-1 truncate font-mono text-xs text-faint">{city.id}</p>
						<p class="mt-2 text-xs text-muted">{city.gridSize} × {city.gridSize} tiles</p>
					</div>
					<div class="mt-auto flex flex-wrap items-center gap-2">
						<Button
							variant={city.status === 'PUBLISHED' ? 'primary' : 'secondary'}
							disabled={opening !== null || !canPlayCity(city)}
							onclick={() => play(city)}
						>
							{opening === city.id ? 'Opening…' : 'Play'}
						</Button>
						{#if canEditCity(city)}
							<Button href={`/delivery/editor?id=${encodeURIComponent(city.id)}`} variant="quiet">
								{requiresAdminKey(city) ? 'Admin edit' : 'Edit'}
							</Button>
						{/if}
					</div>
				</li>
			{/each}
		</ul>
	{/if}
</div>
