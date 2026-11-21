<script lang="ts">
	import { getContextClient, queryStore, gql } from '@urql/svelte';
	import { page } from '$app/stores';
	import { MAPS_QUERY } from '$lib/tesla/queries';
	import { fetchMapLayout } from '$lib/tesla/mapData';
	import { countTiles, mapStatusLabel, prettyMapName, type MapLayout, type MapSummary } from '$lib/tesla/maps';
	import { MapSessionStarter } from '$lib/tesla/mapStart.svelte';
	import { getMapEditKey } from '$lib/tesla/mapEditKeys';
	import { account } from '$lib/account/stores/account';
	import { buildEditWithAIPrompt, graphqlEndpointUrl } from '$lib/tesla/editWithAI';
	import MapThumb from '$lib/tesla/components/MapThumb.svelte';
	import StartSessionModal from '$lib/tesla/components/StartSessionModal.svelte';

	const client = getContextClient();
	const mapsResult = queryStore({ client, query: gql(MAPS_QUERY) });

	const mapId = $derived(decodeURIComponent($page.params.id ?? ''));

	const summary = $derived<MapSummary | null>(
		($mapsResult?.data?.maps ?? []).find((m: MapSummary) => m.mapId === mapId) ?? null
	);

	const starter = new MapSessionStarter(client);

	// undefined = loading, null = failed.
	let layout = $state<MapLayout | null | undefined>(undefined);

	$effect(() => {
		const id = mapId;
		if (!id) return;
		layout = undefined;
		void fetchMapLayout(client, id).then((l) => (layout = l));
	});

	const displayName = $derived(prettyMapName(summary?.name || layout?.name || mapId));
	const gridSize = $derived(summary?.gridSize ?? layout?.gridSize ?? 0);
	const maxBattery = $derived(summary?.maxBattery ?? layout?.maxBattery ?? 0);
	const startingBattery = $derived(layout?.startingBattery ?? 0);
	const description = $derived(summary?.description || layout?.description || '');
	const statusLabel = $derived(summary ? mapStatusLabel(summary) : '');
	const ownedByMe = $derived(summary?.ownedByMe === true);
	const signedIn = $derived($account.me !== null);

	const stats = $derived.by(() => {
		if (!layout) return [];
		return [
			{ label: 'Parks', value: countTiles(layout, 'park'), icon: '🌳' },
			{ label: 'Chargers', value: countTiles(layout, 'supercharger'), icon: '⚡' },
			{ label: 'Homes', value: countTiles(layout, 'home'), icon: '⌂' },
			{ label: 'Obstacles', value: countTiles(layout, 'water') + countTiles(layout, 'building'), icon: '▨' }
		];
	});

	// Only report "not found" once both the map list and the layout lookup have resolved.
	const notFound = $derived(
		!$mapsResult.fetching && $mapsResult.error === undefined && layout === null && summary === null
	);

	// Legacy edit keys live in localStorage. Surface one on the page so the user
	// can see (and hand to an AI) the token that claims the map. Account-owned
	// maps need no token.
	const editKey = $derived(mapId ? getMapEditKey(mapId) : null);

	const aiPrompt = $derived(
		buildEditWithAIPrompt({
			map: {
				mapId,
				name: displayName,
				gridSize,
				maxBattery
			},
			editKey,
			owned: ownedByMe,
			apiUrl: graphqlEndpointUrl()
		})
	);

	let copied = $state<'idle' | 'link' | 'key' | 'prompt' | 'failed'>('idle');

	async function copyText(text: string, what: 'link' | 'key' | 'prompt') {
		try {
			await navigator.clipboard.writeText(text);
			copied = what;
		} catch {
			copied = 'failed';
		}
		setTimeout(() => (copied = 'idle'), 2500);
	}

	async function share() {
		await copyText($page.url.href, 'link');
	}
</script>

<svelte:head>
	<title>{notFound ? 'Map not found' : `${displayName} — Tesla Road Trip`}</title>
	{#if !notFound && description}
		<meta name="description" content={description} />
	{/if}
</svelte:head>

<div class="bg-white border-b border-[#e8e8e8]">
	<div class="max-w-7xl mx-auto px-6 py-8">
		<a href="/tesla/maps" class="text-sm text-gray-500 hover:text-[#393c41]">← All maps</a>
		{#if notFound}
			<h1 class="text-3xl font-light text-[#171a20] mt-4">Map not found</h1>
			<p class="text-gray-500 mt-2">
				There is no map called <code class="text-[#393c41]">{mapId}</code>. It may have been renamed or deleted.
			</p>
			<a href="/tesla/maps" class="inline-block mt-6 bg-[#393c41] text-white text-sm px-5 py-2.5 rounded-full hover:bg-black transition-colors">Browse all maps</a>
		{:else}
			<div class="mt-4 flex flex-col lg:flex-row lg:items-end lg:justify-between gap-6">
				<div class="min-w-0">
					<div class="flex flex-wrap items-center gap-2 mb-3">
						<p class="text-xs font-bold uppercase tracking-widest text-red-600">🗺️ Map</p>
						{#if statusLabel}
							<span
								class={`rounded-full px-2 py-0.5 text-[11px] font-medium ${
									statusLabel === 'Draft' ? 'bg-amber-100 text-amber-900' : 'bg-emerald-50 text-emerald-800'
								}`}
								data-testid="map-status"
							>{statusLabel}</span>
						{/if}
						{#if ownedByMe}
							<span class="rounded-full bg-[#fbeae5] text-[#962d26] px-2 py-0.5 text-[11px] font-medium" data-testid="map-yours">Your map</span>
						{/if}
					</div>
					<h1 class="text-3xl lg:text-5xl font-light text-[#171a20] tracking-tight">{displayName}</h1>
					{#if description}
						<p class="text-lg text-gray-500 font-light max-w-2xl mt-3">{description}</p>
					{/if}
					{#if statusLabel === 'Draft'}
						<p class="text-sm text-amber-800 mt-2">Draft: only you can see and play this map. Publish it when the layout is ready.</p>
					{/if}
				</div>
				<div class="flex flex-wrap gap-3">
					<button
						type="button"
						onclick={() => starter.open({ mapId, name: displayName, description, gridSize, maxBattery })}
						disabled={starter.starting || layout === undefined}
						class="bg-[#393c41] text-white text-sm px-6 py-3 rounded-full hover:bg-black transition-colors disabled:opacity-50"
					>{starter.starting ? 'Starting…' : 'Play this map'}</button>
					<button
						type="button"
						onclick={share}
						class="border border-gray-200 bg-white text-[#393c41] text-sm px-6 py-3 rounded-full hover:border-gray-400 transition-colors"
					>{copied === 'link' ? 'Link copied ✓' : copied === 'failed' ? 'Copy failed' : 'Copy link'}</button>
				</div>
			</div>
		{/if}
	</div>
</div>

{#if !notFound}
	<div class="max-w-7xl mx-auto px-6 py-10">
		{#if $mapsResult.fetching && layout === undefined}
			<div class="grid gap-6 lg:grid-cols-[minmax(0,1fr)_20rem]">
				<div class="aspect-square w-full rounded-2xl bg-white border border-[#e8e8e8] animate-pulse"></div>
				<div class="h-64 rounded-2xl bg-white border border-[#e8e8e8] animate-pulse"></div>
			</div>
		{:else}
			<div class="grid gap-6 lg:grid-cols-[minmax(0,1fr)_20rem]">
				<section class="bg-white rounded-2xl border border-[#e8e8e8] shadow-sm p-4 sm:p-6">
					{#if layout}
						<MapThumb map={layout} label={`Layout of ${displayName}`} />
					{:else}
						<div class="aspect-square w-full rounded-xl bg-red-50 flex items-center justify-center text-sm text-red-700 p-6 text-center">
							The layout for this map could not be loaded.
						</div>
					{/if}
				</section>

				<aside class="space-y-6">
					<section class="bg-white rounded-2xl border border-[#e8e8e8] shadow-sm p-6">
						<h2 class="text-lg font-light text-[#393c41] mb-4">Details</h2>
						<dl class="space-y-2 text-sm">
							<div class="flex items-baseline justify-between gap-3">
								<dt class="text-gray-500">Grid</dt>
								<dd class="text-[#393c41] tabular-nums">{gridSize ? `${gridSize}×${gridSize}` : '—'}</dd>
							</div>
							<div class="flex items-baseline justify-between gap-3">
								<dt class="text-gray-500">Battery</dt>
								<dd class="text-[#393c41] tabular-nums">{startingBattery || '—'} / {maxBattery || '—'}</dd>
							</div>
							<div class="flex items-baseline justify-between gap-3">
								<dt class="text-gray-500">Map ID</dt>
								<dd class="text-[#393c41] font-mono text-xs">{mapId}</dd>
							</div>
						</dl>
						{#if stats.length}
							<div class="mt-4 grid grid-cols-2 gap-2">
								{#each stats as stat}
									<div class="rounded-xl bg-[#f7f7f7] px-3 py-2">
										<p class="text-[11px] text-gray-500">{stat.icon} {stat.label}</p>
										<p class="text-base font-medium text-[#171a20] tabular-nums">{stat.value}</p>
									</div>
								{/each}
							</div>
						{/if}
					</section>

					<section class="bg-white rounded-2xl border border-[#e8e8e8] shadow-sm p-6">
						<h2 class="text-lg font-light text-[#393c41] mb-4">Legend</h2>
						<ul class="space-y-1.5 text-xs text-gray-600">
							<li class="flex items-center gap-2"><span class="h-4 w-4 rounded-sm bg-red-500"></span>Home — charge to full</li>
							<li class="flex items-center gap-2"><span class="h-4 w-4 rounded-sm bg-emerald-500"></span>Park — visit them all to win</li>
							<li class="flex items-center gap-2"><span class="h-4 w-4 rounded-sm bg-yellow-400"></span>Supercharger — partial charge</li>
							<li class="flex items-center gap-2"><span class="h-4 w-4 rounded-sm bg-blue-400"></span>Water — impassable</li>
							<li class="flex items-center gap-2"><span class="h-4 w-4 rounded-sm bg-slate-700"></span>Blocked — impassable</li>
						</ul>
					</section>

					<section class="bg-white rounded-2xl border border-[#e8e8e8] shadow-sm p-6">
						<h2 class="text-lg font-light text-[#393c41] mb-2">Share this map</h2>
						<p class="text-xs text-gray-500">
							{#if statusLabel === 'Draft'}
								This draft is visible only to you. Publish it before sharing; this link will work for everyone then.
							{:else}
								Send this page's link and your friend can start a session on {displayName} straight away.
							{/if}
						</p>
						<button
							type="button"
							onclick={share}
							class="mt-4 w-full border border-gray-200 bg-white text-[#393c41] text-sm px-4 py-2.5 rounded-full hover:border-gray-400 transition-colors"
						>{copied === 'link' ? 'Link copied ✓' : copied === 'failed' ? 'Copy failed' : 'Copy link'}</button>
					</section>

					<section class="bg-white rounded-2xl border border-[#e8e8e8] shadow-sm p-6" data-testid="edit-key-section">
						<h2 class="text-lg font-light text-[#393c41] mb-2">{ownedByMe ? 'Your map' : 'Legacy edit key'}</h2>
						{#if ownedByMe}
							<p class="text-xs text-gray-500">
								{displayName} belongs to your account. Open the editor to change it; no edit key is needed.
								Saving an edit puts the map back into draft until you publish it again.
							</p>
							<a
								href={`/tesla/editor?map=${encodeURIComponent(mapId)}`}
								class="mt-4 inline-block border border-gray-200 bg-white text-[#393c41] text-sm px-4 py-2.5 rounded-full hover:border-gray-400 transition-colors"
							>Edit this map</a>
						{:else if summary?.builtin}
							<p class="text-xs text-gray-500">
								{displayName} ships with the server. Only an administrator can change it; use the editor to save
								your own copy instead.
							</p>
						{:else if editKey}
							<p class="text-xs text-gray-500">
								This map was created before accounts existed. The key below claims it to your account and still
								authorizes edits until it is claimed. Keep it if you want to claim the map later.
							</p>
							<div class="mt-3 flex items-start gap-2">
								<code class="flex-1 min-w-0 break-all rounded-xl bg-[#f7f7f7] px-3 py-2 text-xs text-[#393c41] font-mono" data-testid="edit-key-value">{editKey}</code>
								<button
									type="button"
									onclick={() => copyText(editKey, 'key')}
									class="shrink-0 border border-gray-200 bg-white text-[#393c41] text-xs px-3 py-2 rounded-full hover:border-gray-400 transition-colors"
								>{copied === 'key' ? 'Copied ✓' : 'Copy'}</button>
							</div>
							<a
								href={`/tesla/editor?map=${encodeURIComponent(mapId)}`}
								class="mt-4 inline-block border border-gray-200 bg-white text-[#393c41] text-sm px-4 py-2.5 rounded-full hover:border-gray-400 transition-colors"
							>Claim in the editor</a>
						{:else}
							<p class="text-xs text-gray-500">
								{#if $account.loaded && !signedIn}
									Sign in to check whether this map belongs to you; otherwise it belongs to another player, or
									it predates accounts and can only be claimed by the browser that created it.
								{:else}
									This map belongs to another player, or it predates accounts and this browser has no legacy
									key for it. Only its creator can change it; use the editor to save your own copy instead.
								{/if}
							</p>
						{/if}
					</section>

					<section class="bg-white rounded-2xl border border-[#e8e8e8] shadow-sm p-6" data-testid="edit-with-ai-section">
						<h2 class="text-lg font-light text-[#393c41] mb-2">Edit with AI</h2>
						<p class="text-xs text-gray-500">
							Copy this prompt into an AI agent. It includes the GraphQL endpoint and this map's id.
							{#if ownedByMe}
								It explains that saving needs your signed-in account's bearer token.
							{:else if editKey}
								It includes the legacy edit key, so the agent can claim or edit the map.
							{:else}
								It tells the agent to ask you for the legacy edit key before saving.
							{/if}
						</p>
						<button
							type="button"
							onclick={() => copyText(aiPrompt, 'prompt')}
							class="mt-4 w-full bg-[#393c41] text-white text-sm px-4 py-2.5 rounded-full hover:bg-black transition-colors"
						>{copied === 'prompt' ? 'Prompt copied ✓' : 'Copy AI prompt'}</button>
						<details class="mt-3">
							<summary class="cursor-pointer text-xs text-gray-500 hover:text-[#393c41]">Preview prompt</summary>
							<pre class="mt-2 max-h-64 overflow-auto rounded-xl bg-[#f7f7f7] p-3 text-[11px] leading-relaxed text-[#393c41] whitespace-pre-wrap" data-testid="ai-prompt-preview">{aiPrompt}</pre>
						</details>
					</section>
				</aside>
			</div>
		{/if}
	</div>
{/if}

{#if starter.map}
	<StartSessionModal
		map={starter.map}
		starting={starter.starting}
		error={starter.error}
		onstart={(settings) => starter.begin(settings)}
		onclose={() => starter.cancel()}
	/>
{/if}
