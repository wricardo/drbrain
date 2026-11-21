<script lang="ts">
	import { initials } from '$lib/account/account';
	import Button from '@drbrain/ui/components/Button.svelte';
	import EmptyState from '@drbrain/ui/components/EmptyState.svelte';
	import Skeleton from '@drbrain/ui/components/Skeleton.svelte';
	import { page } from '$app/stores';
	import { getContextClient } from '@urql/svelte';
	import { unwrap } from '@drbrain/ui/graphql';
	import { summarizeProfile, tallyText } from '$lib/motor/profile';
	import { describeError, type DescribedError } from '@drbrain/ui/errors';
	import { boardHref, rankBadgeClass, rankLabel } from '$lib/motor/leaderboard';
	import { MAPS_QUERY, PLAYER_RUNS_FOR_QUERY } from '$lib/motor/queries';
	import { PROFILE_QUERY } from '$lib/account/queries';
	import { runText } from '$lib/motor/runs';
	import { account } from '$lib/account/stores/account';
	import { absoluteTime, relativeTime } from '@drbrain/ui/time';
	import type { GameMap, PlayerRun } from '$lib/motor/types';
	import type { Profile } from '$lib/account/types';

	const client = getContextClient();
	const handle = $derived($page.params.handle ?? '');

	let maps = $state<GameMap[]>([]);
	let accountProfile = $state<Profile | null>(null);
	let runs = $state<PlayerRun[]>([]);
	let loading = $state(true);
	let error = $state<DescribedError | null>(null);
	let runsError = $state<DescribedError | null>(null);
	const now = new Date();

	const profile = $derived(accountProfile ? { ...accountProfile, runs } : null);
	const summary = $derived(profile && !runsError ? summarizeProfile(maps, runs) : null);
	const joined = $derived(profile ? new Date(profile.createdAt).toLocaleDateString(undefined, { year: 'numeric', month: 'long', day: 'numeric' }) : '');
	const mapName = (id: string) => maps.find((m) => m.id === id)?.name ?? id;

	let requestId = 0;
	async function load(h: string) {
		const mine = ++requestId;
		loading = true;
		error = null;
		runsError = null;
		accountProfile = null;
		runs = [];
		try {
			const { account } = await unwrap<{ account: { profile: Profile | null } }>(
				client.query(PROFILE_QUERY, { handle: h }, { requestPolicy: 'network-only' })
			);
			if (mine !== requestId || !account.profile) return;
			accountProfile = account.profile;
			try {
				const [m, r] = await Promise.all([
					maps.length ? Promise.resolve({ maps }) : unwrap<{ maps: GameMap[] }>(client.query(MAPS_QUERY, {})),
					unwrap<{ playerRunsFor: PlayerRun[] }>(
						client.query(PLAYER_RUNS_FOR_QUERY, { handle: h }, { requestPolicy: 'network-only' })
					)
				]);
				if (mine !== requestId) return;
				maps = m.maps;
				runs = r.playerRunsFor;
			} catch (e) {
				if (mine === requestId) runsError = describeError(e);
			}
		} catch (e) {
			if (mine === requestId) error = describeError(e);
		} finally {
			if (mine === requestId) loading = false;
		}
	}

	// Reload when the handle changes or once the viewer is known (a private profile shows only to its owner).
	$effect(() => {
		const h = handle;
		if ($account.loaded && h) void load(h);
	});
</script>

<svelte:head><title>{profile?.displayName ?? 'Player profile'} · drbrain</title></svelte:head>

<div class="db-container profile">
	{#if error}
		<p role="alert" class="rounded-lg border border-rose-300 bg-rose-50 px-4 py-3 text-sm text-rose-900">
			<strong>{error.title}.</strong> {error.message}
		</p>
	{:else if loading}
		<Skeleton label="Loading profile…" />
	{:else if !profile}
		<div data-testid="profile-missing">
			<EmptyState title="Profile unavailable" description="There’s no public profile at @{handle}.">
				<Button href="/motor/leaderboard" variant="secondary">See the leaderboard</Button>
			</EmptyState>
		</div>
	{:else}
		<header class="db-panel identity">
			<span class="db-avatar size-16 text-2xl sm:size-20" aria-hidden="true">{initials(profile.displayName, profile.handle)}</span>
			<div class="min-w-0">
				<h1 class="break-words profile-name" data-testid="profile-name">{profile.displayName}</h1>
				<p class="identity-details"><span data-testid="profile-handle">@{profile.handle}</span> · Joined {joined}</p>
				{#if profile.isMine}
					<p class="identity-details" data-testid="profile-mine">{$account.me?.profilePublic ? 'Public profile' : 'Private profile'}</p>
				{/if}
			</div>
			{#if profile.isMine}<Button href="/settings" variant="secondary" class="profile-settings">Settings</Button>{/if}
		</header>

		<div class="activity-title" data-game="motor">
			<h2>Motor Programming</h2>
			<a href="/motor" class="db-text-link">Play <span aria-hidden="true">↗</span></a>
		</div>
		{#if runsError}
			<p class="text-sm text-rose-800" role="alert">Could not load game activity. {runsError.message}</p>
		{/if}

		{#if summary}
			<div class="grid grid-cols-2 gap-3" data-testid="profile-tallies">
				<div class="db-panel px-3 py-4 sm:px-5">
					<p class="db-kicker">Official maps</p>
					<p class="mt-1 text-lg font-semibold text-indigo-950" data-testid="tally-official">{tallyText(summary.official, 'solved')}</p>
				</div>
				<div class="db-panel px-3 py-4 sm:px-5">
					<p class="db-kicker">Community maps</p>
					<p class="mt-1 text-lg font-semibold text-indigo-950" data-testid="tally-community">
						{summary.community.solved} solved
						<span class="text-sm font-normal text-slate-600">of {summary.community.total}</span>
					</p>
				</div>
			</div>

			{#if summary.rows.length === 0}
				<div data-testid="profile-empty">
					<EmptyState title="No maps solved yet" description={profile.isMine ? 'Finish a Motor Programming map to record your first run.' : 'Motor Programming runs will appear here after a map is solved.'}>
						{#if profile.isMine}<Button href="/motor/maps" variant="secondary">Pick a map</Button>{/if}
					</EmptyState>
				</div>
			{:else}
				<section aria-labelledby="best-title" class="space-y-2">
					<h2 id="best-title" class="text-lg font-semibold text-indigo-950">Best run on each map</h2>
					<div class="db-panel relative overflow-x-auto">
						<table class="w-full text-left text-sm" data-testid="profile-table">
							<caption class="sr-only">{profile.displayName}'s best run on each solved map</caption>
							<thead class="bg-indigo-50 text-xs uppercase tracking-wide text-indigo-900">
								<tr>
									<th scope="col" class="px-4 py-2">Map</th>
									<th scope="col" class="px-4 py-2">Best run</th>
									<th scope="col" class="px-4 py-2">Rank</th>
									<th scope="col" class="hidden px-4 py-2 sm:table-cell">When</th>
									<th scope="col" class="px-4 py-2"><span class="sr-only">Actions</span></th>
								</tr>
							</thead>
							<tbody class="divide-y divide-indigo-100">
								{#each summary.rows as r (r.map.id)}
									<tr>
										<td class="px-4 py-2 font-medium">{r.map.name}</td>
										<td class="px-4 py-2 tabular-nums whitespace-nowrap">{runText(r.run)}</td>
										<td class="px-4 py-2">
											<span class="inline-flex items-center gap-2">
												<span
													class="inline-flex h-7 min-w-7 items-center justify-center rounded-full border px-2 text-xs font-semibold tabular-nums {rankBadgeClass(
														r.run.entry.rank
													)}"
													aria-label={rankLabel(r.run.entry.rank)}>{r.run.entry.rank}</span
												>
												<span class="text-xs text-slate-600">of {r.run.total}</span>
											</span>
										</td>
										<td class="hidden px-4 py-2 text-slate-700 sm:table-cell" title={absoluteTime(r.run.entry.achievedAt)}>{relativeTime(r.run.entry.achievedAt, now)}</td>
										<td class="px-4 py-2 whitespace-nowrap text-right">
											<a class="text-indigo-700 underline" href="/motor/watch/{r.run.entry.sessionId}">Watch</a>
											<span aria-hidden="true" class="text-slate-400"> · </span>
											<a class="text-indigo-700 underline" href={boardHref(r.map.id)}>Leaderboard</a>
										</td>
									</tr>
								{/each}
							</tbody>
						</table>
					</div>
				</section>

				<section aria-labelledby="recent-title" class="space-y-2">
					<h2 id="recent-title" class="text-lg font-semibold text-indigo-950">Recent activity</h2>
					<ul class="db-panel divide-y divide-indigo-100 text-sm" data-testid="profile-recent">
						{#each summary.recent as r (r.mapId)}
							<li class="flex flex-wrap items-center justify-between gap-2 px-4 py-2">
								<span>Solved <strong>{mapName(r.mapId)}</strong> in {runText(r)}</span>
								<span class="text-slate-600" title={absoluteTime(r.entry.achievedAt)}>{relativeTime(r.entry.achievedAt, now)}</span>
							</li>
						{/each}
					</ul>
				</section>
			{/if}
		{/if}
		{#if summary?.rows.length}
			<p class="text-xs text-slate-600">Best runs use the “Fewest slots” leaderboard. Signed-in wins and claimed guest runs count toward these results.</p>
		{/if}
	{/if}
</div>


<style>
	.profile { display: grid; gap: 1rem; max-width: 1100px; padding-block: clamp(1.6rem, 5vw, 4.25rem); }
	.identity { display: grid; grid-template-columns: auto minmax(0, 1fr) auto; align-items: center; gap: 1rem; padding: clamp(1.35rem, 4vw, 2.5rem); }
	.profile-name { font-size: clamp(1.85rem, 4vw, 3rem); font-weight: 710; line-height: 1.1; letter-spacing: -.052em; }
	.identity-details { color: var(--db-muted); margin-top: .35rem; font-size: .88rem; }
	.identity :global(.profile-settings) { justify-self: start; }
	@media (max-width: 520px) {
		.identity { grid-template-columns: auto minmax(0, 1fr); }
		.identity :global(.profile-settings) { grid-column: 2; }
	}
	.activity-title { display: flex; justify-content: space-between; align-items: baseline; margin-top: 1rem; gap: .5rem; }
	.activity-title h2 { color: var(--db-ink); font-size: 1.35rem; font-weight: 710; letter-spacing: -.035em; }
	.activity-title a { font-size: .9rem; }
</style>
