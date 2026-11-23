<script lang="ts">
	import { onDestroy, onMount } from 'svelte';
	import { api, ApiError, type SessionSort, type SessionSummary } from '$lib/delivery/api';
	import { pageTitle } from '$lib/delivery/brand';
	import { ownerTokenFor } from '$lib/delivery/ownerTokens';
	import { account } from '$lib/account/stores/account';
	import Button from '@drbrain/ui/components/Button.svelte';
	import EmptyState from '@drbrain/ui/components/EmptyState.svelte';
	import Skeleton from '@drbrain/ui/components/Skeleton.svelte';
	import { absoluteTime, relativeTime } from '@drbrain/ui/time';

	/** Server cap for the sessions query. */
	const LIMIT = 500;
	const PAGE = 25;
	const REFRESH_MS = 10_000;

	const SORTS: [SessionSort, string][] = [
		['CREATED_AT', 'Newest first'],
		['TICK', 'Highest tick'],
		['MONEY', 'Highest balance']
	];

	let sessions = $state<SessionSummary[]>([]);
	let loading = $state(true);
	let failed = $state(false);
	let error = $state<string | null>(null);
	let now = $state(new Date());

	let sort = $state<SessionSort>('CREATED_AT');
	let mineOnly = $state(false);
	let text = $state('');
	let pageSize = $state(PAGE);

	const signedIn = $derived($account.me !== null);

	/** True when this browser or the signed-in account may change the save. */
	function isMine(s: SessionSummary): boolean {
		return s.mine || ownerTokenFor(s.id) !== null;
	}

	/** The player's label for a save, falling back to its owner and then an id suffix. */
	function saveName(s: SessionSummary): string {
		if (s.displayName) return s.displayName;
		if (isMine(s)) return 'Your city';
		if (s.ownerDisplayName !== 'Guest') return `${s.ownerDisplayName}’s city`;
		return `City …${s.id.slice(-6)}`;
	}

	const shown = $derived.by(() => {
		const needle = text.trim().toLowerCase();
		if (!needle) return sessions;
		return sessions.filter((s) =>
			[saveName(s), s.ownerDisplayName, s.ownerHandle ?? '', s.id].some((v) =>
				v.toLowerCase().includes(needle)
			)
		);
	});
	const visible = $derived(shown.slice(0, pageSize));

	async function load(quiet = false) {
		if (!quiet) loading = true;
		failed = false;
		error = null;
		try {
			sessions = await api.sessions({
				sort,
				limit: LIMIT,
				mine: signedIn && mineOnly ? true : undefined
			});
			now = new Date();
		} catch (e) {
			failed = true;
			error = e instanceof ApiError ? `${e.message} (${e.code})` : String(e);
		} finally {
			loading = false;
		}
	}

	let timer: ReturnType<typeof setInterval> | undefined;
	onMount(() => {
		void load();
		timer = setInterval(() => void load(true), REFRESH_MS);
	});
	onDestroy(() => clearInterval(timer));

	function clearFilters() {
		text = '';
		mineOnly = false;
		pageSize = PAGE;
	}
</script>

<svelte:head>
	<title>{pageTitle('Sessions')}</title>
</svelte:head>

<div class="db-container py-8">
	<header class="mb-6 flex flex-wrap items-end justify-between gap-4">
		<div>
			<p class="db-kicker">Delivery</p>
			<h1 class="text-3xl font-bold tracking-tight">Sessions</h1>
			<p class="mt-1 max-w-2xl text-sm text-muted">
				Every saved city on this server. Open one to play it if it is yours, or to watch.
			</p>
		</div>
		<div class="flex items-center gap-2">
			<span class="text-xs text-faint">Auto-refreshes every 10 s</span>
			<Button variant="secondary" disabled={loading} onclick={() => load()}>Refresh</Button>
		</div>
	</header>

	<div class="mb-4 flex flex-wrap items-center gap-3">
		<label class="sr-only" for="f-text">Search sessions</label>
		<input
			id="f-text"
			type="search"
			class="db-input w-full sm:w-64"
			bind:value={text}
			oninput={() => (pageSize = PAGE)}
			placeholder="Search name, owner or id"
		/>
		<label class="flex items-center gap-2 text-sm text-muted" for="f-sort">
			Sort
			<select
				id="f-sort"
				class="db-input"
				bind:value={sort}
				onchange={() => {
					pageSize = PAGE;
					void load();
				}}
			>
				{#each SORTS as [value, label] (value)}
					<option {value}>{label}</option>
				{/each}
			</select>
		</label>
		{#if signedIn}
			<label class="flex items-center gap-2 text-sm text-muted">
				<input
					type="checkbox"
					class="h-4 w-4"
					bind:checked={mineOnly}
					onchange={() => {
						pageSize = PAGE;
						void load();
					}}
				/>
				Only mine
			</label>
		{/if}
	</div>

	{#if error}
		<p
			class="mb-4 rounded-lg border border-[var(--db-danger)]/40 bg-[var(--db-danger)]/10 px-4 py-3 text-sm text-[var(--db-danger)]"
			role="alert"
		>
			{error}
		</p>
	{/if}

	<p class="mb-3 text-sm text-faint" role="status" aria-live="polite">
		{#if loading}
			Loading sessions…
		{:else}
			Showing {visible.length} of {shown.length}{shown.length !== sessions.length
				? ` matching (${sessions.length} loaded)`
				: ''}{sessions.length >= LIMIT
				? ` — the server returns at most ${LIMIT}`
				: ''}.
		{/if}
	</p>

	{#if loading}
		<Skeleton label="Loading sessions…" />
	{:else if failed}
		<EmptyState title="Could not load sessions" description="Try loading the list again.">
			<Button variant="secondary" onclick={() => load()}>Retry</Button>
		</EmptyState>
	{:else if shown.length === 0}
		<EmptyState
			title={sessions.length === 0 ? 'No sessions yet' : 'No sessions match these filters'}
			description={sessions.length === 0
				? 'Start a city to see it here.'
				: 'Change the search or filters.'}
		>
			{#if sessions.length === 0}
				<Button href="/delivery">Start a city <span aria-hidden="true">→</span></Button>
			{:else}
				<Button variant="secondary" onclick={clearFilters}>Clear filters</Button>
			{/if}
		</EmptyState>
	{:else}
		<div class="db-panel overflow-x-auto">
			<table class="w-full text-left text-sm">
				<caption class="sr-only">Saved delivery sessions</caption>
				<thead class="border-b border-edge text-[11px] uppercase tracking-wide text-faint">
					<tr>
						<th scope="col" class="px-4 py-2">Session</th>
						<th scope="col" class="px-4 py-2">State</th>
						<th scope="col" class="px-4 py-2 text-right">Tick</th>
						<th scope="col" class="px-4 py-2 text-right">Balance</th>
						<th scope="col" class="px-4 py-2 text-right">Rep</th>
						<th scope="col" class="px-4 py-2 text-right">Delivered</th>
						<th scope="col" class="px-4 py-2">Created</th>
						<th scope="col" class="px-4 py-2"><span class="sr-only">Actions</span></th>
					</tr>
				</thead>
				<tbody class="divide-y divide-edge">
					{#each visible as s (s.id)}
						{@const mine = isMine(s)}
						<tr>
							<td class="min-w-0 px-4 py-2">
								<span class="block truncate text-body">{saveName(s)}</span>
								<span class="block truncate text-[11px] text-faint">
									{#if mine}
										yours · {s.id}
									{:else if s.ownerHandle}
										<a class="hover:text-body" href={`/u/${s.ownerHandle}`}>@{s.ownerHandle}</a>
										· {s.id}
									{:else}
										{s.ownerDisplayName} · {s.id}
									{/if}
								</span>
							</td>
							<td class="px-4 py-2">
								<span
									class="rounded border px-2 py-0.5 text-[11px] font-semibold {s.loaded
										? 'border-[var(--db-success)] text-[var(--db-success)]'
										: 'border-edge-strong text-muted'}"
								>
									{s.loaded ? (s.speed > 0 ? 'running' : 'loaded') : 'saved'}
								</span>
							</td>
							<td class="px-4 py-2 text-right tabular-nums">{s.tick}</td>
							<td class="px-4 py-2 text-right tabular-nums text-[var(--color-money)]">${s.money}</td>
							<td class="px-4 py-2 text-right tabular-nums">{s.reputation.toFixed(2)}</td>
							<td class="px-4 py-2 text-right tabular-nums">{s.deliveries}</td>
							<td class="px-4 py-2 text-muted" title={absoluteTime(s.createdAt)}>
								{relativeTime(s.createdAt, now)}
							</td>
							<td class="px-4 py-2 whitespace-nowrap text-right">
								<a class="font-semibold text-[var(--db-accent)] underline" href={`/delivery?city=${encodeURIComponent(s.id)}`}>
									{mine ? 'Play' : 'Watch'}
								</a>
							</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
		{#if shown.length > visible.length}
			<div class="mt-4 flex justify-center">
				<Button variant="secondary" onclick={() => (pageSize += PAGE)}>
					Load more ({shown.length - visible.length} more)
				</Button>
			</div>
		{/if}
	{/if}
</div>
