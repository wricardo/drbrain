<script lang="ts">
	import { pageTitle } from '$lib/motor/brand';
	import { onMount } from 'svelte';
	import { page } from '$app/stores';
	import { getContextClient } from '@urql/svelte';
	import { unwrap } from '$lib/motor/api';
	import { summarizeProfile, tallyText } from '$lib/motor/account';
	import { describeError, type DescribedError } from '$lib/motor/errors';
	import { boardHref, rankBadgeClass, rankLabel } from '$lib/motor/leaderboard';
	import { MAPS_QUERY, PROFILE_QUERY } from '$lib/motor/queries';
	import { runText } from '$lib/motor/runs';
	import { account } from '$lib/motor/stores/account';
	import { absoluteTime, relativeTime } from '$lib/motor/time';
	import type { GameMap, Profile } from '$lib/motor/types';

	const client = getContextClient();
	const handle = $derived($page.params.handle ?? '');

	let maps = $state<GameMap[]>([]);
	let profile = $state<Profile | null>(null);
	let loading = $state(true);
	let error = $state<DescribedError | null>(null);
	const now = new Date();

	const summary = $derived(profile ? summarizeProfile(maps, profile.runs) : null);
	const joined = $derived(profile ? new Date(profile.createdAt).toLocaleDateString(undefined, { year: 'numeric', month: 'long', day: 'numeric' }) : '');
	const mapName = (id: string) => maps.find((m) => m.id === id)?.name ?? id;

	let requestId = 0;
	async function load(h: string) {
		const mine = ++requestId;
		loading = true;
		error = null;
		profile = null;
		try {
			const [m, p] = await Promise.all([
				maps.length ? Promise.resolve({ maps }) : unwrap<{ maps: GameMap[] }>(client.query(MAPS_QUERY, {})),
				unwrap<{ profile: Profile | null }>(client.query(PROFILE_QUERY, { handle: h }, { requestPolicy: 'network-only' }))
			]);
			if (mine !== requestId) return;
			maps = m.maps;
			profile = p.profile;
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

	onMount(() => {
		// Nothing to do: the effect above starts the first load once the account state is known.
	});
</script>

<svelte:head><title>{pageTitle(profile ? `${profile.displayName} (@${profile.handle})` : 'Profile')}</title></svelte:head>

<div class="mx-auto max-w-6xl px-4 sm:px-6 py-8 space-y-5">
	{#if error}
		<p role="alert" class="rounded-lg border border-rose-300 bg-rose-50 px-3 py-2 text-sm text-rose-900">
			<strong>{error.title}.</strong> {error.message}
		</p>
	{:else if loading}
		<p class="text-sm text-slate-600" role="status">Loading profile…</p>
	{:else if !profile}
		<div class="rounded-xl border border-indigo-200 bg-white px-4 py-8 text-center" data-testid="profile-missing">
			<h1 class="text-xl font-semibold text-indigo-950">No profile here</h1>
			<p class="mt-2 text-sm text-slate-700">
				There is no public profile with the handle <strong>@{handle}</strong>. It may have been renamed, made private or deleted.
			</p>
			<p class="mt-4"><a href="/motor/leaderboard" class="text-indigo-700 underline">See the leaderboard</a></p>
		</div>
	{:else}
		<header class="flex flex-wrap items-start justify-between gap-3">
			<div class="min-w-0">
				<h1 class="break-words text-2xl font-semibold text-indigo-950" data-testid="profile-name">{profile.displayName}</h1>
				<p class="mt-1 text-sm text-slate-700">
					<span data-testid="profile-handle">@{profile.handle}</span> · Joined {joined}
				</p>
				{#if profile.isMine}
					<p class="mt-2 text-sm text-slate-700" data-testid="profile-mine">
						This is your profile.
						{#if $account.me && !$account.me.profilePublic}
							<strong class="text-indigo-950">It is private: only you can see this page.</strong>
						{:else}
							Everyone can see it.
						{/if}
						<a href="/motor/settings" class="text-indigo-700 underline">Settings</a>
					</p>
				{/if}
			</div>
		</header>

		{#if summary}
			<div class="grid gap-3 sm:grid-cols-2" data-testid="profile-tallies">
				<div class="rounded-xl border border-indigo-200 bg-white px-4 py-3">
					<p class="text-xs font-semibold uppercase tracking-widest text-indigo-900">Official maps</p>
					<p class="mt-1 text-lg font-semibold text-indigo-950" data-testid="tally-official">{tallyText(summary.official, 'solved')}</p>
				</div>
				<div class="rounded-xl border border-indigo-200 bg-white px-4 py-3">
					<p class="text-xs font-semibold uppercase tracking-widest text-indigo-900">Community maps</p>
					<p class="mt-1 text-lg font-semibold text-indigo-950" data-testid="tally-community">
						{summary.community.solved} solved
						<span class="text-sm font-normal text-slate-600">of {summary.community.total}</span>
					</p>
				</div>
			</div>

			{#if summary.rows.length === 0}
				<p class="rounded-xl border border-indigo-200 bg-white px-4 py-6 text-sm text-slate-600" data-testid="profile-empty">
					No maps solved yet.
					{#if profile.isMine}<a href="/motor/maps" class="text-indigo-700 underline">Pick a map</a> and play while signed in, or add your guest runs from Settings.{/if}
				</p>
			{:else}
				<section aria-labelledby="best-title" class="space-y-2">
					<h2 id="best-title" class="text-lg font-semibold text-indigo-950">Best run on each map</h2>
					<div class="relative overflow-x-auto rounded-xl border border-indigo-200 bg-white">
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
					<ul class="divide-y divide-indigo-100 rounded-xl border border-indigo-200 bg-white text-sm" data-testid="profile-recent">
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
		<p class="text-xs text-slate-600">
			Each map shows the player's best result on the “Fewest slots” board. Only wins played while signed in, or added from this browser's guest
			runs, appear here.
		</p>
	{/if}
</div>
