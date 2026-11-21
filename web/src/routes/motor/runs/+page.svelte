<script lang="ts">
	import { pageTitle } from '$lib/motor/brand';
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { getContextClient } from '@urql/svelte';
	import { unwrap } from '@drbrain/ui/graphql';
	import { describeError, type DescribedError } from '@drbrain/ui/errors';
	import { boardHref, rankBadgeClass, rankLabel } from '$lib/motor/leaderboard';
	import { isOfficial } from '$lib/motor/maps';
	import { CREATE_SESSION_MUTATION, MAPS_QUERY, PLAYER_RUNS_QUERY } from '$lib/motor/queries';
	import {
		RUN_FILTERS,
		RUN_FILTER_LABELS,
		buildRunRows,
		countRows,
		filterRows,
		runText,
		solvedSummary,
		type RunFilter
	} from '$lib/motor/runs';
	import { ownSessionIds, rememberSession } from '$lib/motor/sessions';
	import { account } from '$lib/account/stores/account';
	import { absoluteTime, relativeTime } from '@drbrain/ui/time';
	import type { GameMap, PlayerRun, Session } from '$lib/motor/types';

	const client = getContextClient();
	let maps = $state<GameMap[]>([]);
	let runs = $state<PlayerRun[]>([]);
	let ready = $state(false);
	let loading = $state(true);
	let busy = $state('');
	let error = $state<DescribedError | null>(null);
	let filter = $state<RunFilter>('all');
	let ownCount = $state(0);
	const now = new Date();

	const signedIn = $derived($account.me !== null);
	const rows = $derived(buildRunRows(maps, runs));
	const counts = $derived(countRows(rows));
	const shown = $derived(filterRows(rows, filter));

	let requestId = 0;
	async function load(withAccount: boolean) {
		const mineRequest = ++requestId;
		loading = true;
		error = null;
		try {
			const ids = ownSessionIds();
			ownCount = ids.length;
			if (ids.length > 0 || withAccount) {
				// Signed in, the server adds the account's own runs to this browser's sessions.
				runs = (await unwrap<{ playerRuns: PlayerRun[] }>(client.query(PLAYER_RUNS_QUERY, { sessionIds: ids }, { requestPolicy: 'network-only' }))).playerRuns;
			} else {
				runs = [];
			}
		} catch (e) {
			if (mineRequest === requestId) error = describeError(e);
		} finally {
			if (mineRequest === requestId) loading = false;
		}
	}

	onMount(async () => {
		try {
			maps = (await unwrap<{ maps: GameMap[] }>(client.query(MAPS_QUERY, {}))).maps;
		} catch (e) {
			error = describeError(e);
		}
		ready = true;
	});

	$effect(() => {
		const withAccount = signedIn;
		if (ready && $account.loaded) void load(withAccount);
	});

	async function play(map: GameMap) {
		busy = map.id;
		error = null;
		try {
			const data = await unwrap<{ createSession: Session }>(
				client.mutation(CREATE_SESSION_MUTATION, { mapID: map.id })
			);
			rememberSession(data.createSession.id, data.createSession.ownerToken);
			await goto(`/motor/play/${data.createSession.id}`);
		} catch (e) {
			error = describeError(e);
		} finally {
			busy = '';
		}
	}

	const difficultyClass: Record<string, string> = {
		easy: 'bg-emerald-100 text-emerald-900',
		medium: 'bg-amber-100 text-amber-900',
		hard: 'bg-rose-100 text-rose-900'
	};
</script>

<svelte:head><title>{pageTitle('My runs')}</title></svelte:head>

<div class="mx-auto max-w-6xl px-4 sm:px-6 py-8 space-y-5">
	<header class="flex flex-wrap items-end justify-between gap-3">
		<div>
			<h1 class="text-2xl font-semibold text-indigo-950" data-testid="runs-title">My runs</h1>
			<p class="mt-1 text-sm text-slate-700">Your best run on every map you have won, and how it ranks.</p>
		</div>
	</header>

	{#if error}
		<p role="alert" class="rounded-lg border border-rose-300 bg-rose-50 px-3 py-2 text-sm text-rose-900">
			<strong>{error.title}.</strong> {error.message}
		</p>
	{/if}

	{#if !loading && ownCount === 0 && !signedIn}
		<p class="rounded-xl border border-indigo-200 bg-white px-4 py-4 text-sm text-slate-700" data-testid="runs-empty">
			This browser has not played yet. Pick a map below and your runs will show up here. They are remembered in this browser, so a different
			browser starts empty.
		</p>
	{/if}

	<div class="flex flex-wrap items-center gap-3">
		<div class="flex flex-wrap gap-1.5" role="group" aria-label="Show">
			{#each RUN_FILTERS as f (f)}
				<button
					type="button"
					aria-pressed={filter === f}
					class="rounded-full border px-3 py-1 text-sm {filter === f
						? 'border-indigo-600 bg-indigo-600 text-white'
						: 'border-indigo-200 bg-white text-indigo-900 hover:bg-indigo-50'}"
					onclick={() => (filter = f)}>{RUN_FILTER_LABELS[f]} <span class="opacity-75">({counts[f]})</span></button
				>
			{/each}
		</div>
		<p class="text-sm font-medium text-indigo-950" role="status" aria-live="polite" data-testid="runs-summary">
			{loading ? 'Loading…' : solvedSummary(rows)}
		</p>
	</div>

	{#if shown.length > 0}
		<div class="relative overflow-x-auto rounded-xl border border-indigo-200 bg-white">
			<table class="w-full text-left text-sm" data-testid="runs-table">
				<caption class="sr-only">My best run on each map</caption>
				<thead class="bg-indigo-50 text-xs uppercase tracking-wide text-indigo-900">
					<tr>
						<th scope="col" class="px-4 py-2">Map</th>
						<th scope="col" class="px-4 py-2">Status</th>
						<th scope="col" class="px-4 py-2">Best run</th>
						<th scope="col" class="px-4 py-2">Rank</th>
						<th scope="col" class="px-4 py-2">When</th>
						<th scope="col" class="px-4 py-2"><span class="sr-only">Actions</span></th>
					</tr>
				</thead>
				<tbody class="divide-y divide-indigo-100">
					{#each shown as r, i (r.map.id)}
						{#if !isOfficial(r.map) && (i === 0 || isOfficial(shown[i - 1].map))}
							<tr data-testid="runs-community-heading" class="bg-indigo-50/60">
								<th scope="colgroup" colspan="6" class="px-4 py-2 text-xs font-semibold uppercase tracking-wide text-indigo-900">
									Community maps
									<span class="font-normal normal-case tracking-normal text-slate-600">· made by players</span>
								</th>
							</tr>
						{/if}
						<tr data-solved={r.run ? 'true' : 'false'}>
							<td class="px-4 py-2">
								<span class="font-medium">{r.map.name}</span>
								<span class="ml-1 rounded-full px-2 py-0.5 text-xs {difficultyClass[r.map.difficulty] ?? 'bg-indigo-50 text-indigo-800'}"
									>{r.map.difficulty}</span
								>
							</td>
							<td class="px-4 py-2">
								{#if r.run}
									<span class="rounded-full bg-emerald-100 px-2 py-0.5 text-xs font-medium text-emerald-900">Solved</span>
								{:else}
									<span class="rounded-full bg-slate-100 px-2 py-0.5 text-xs text-slate-700">Not yet</span>
								{/if}
							</td>
							<td class="px-4 py-2 tabular-nums">{r.run ? runText(r.run) : '—'}</td>
							<td class="px-4 py-2">
								{#if r.run}
									<span class="inline-flex items-center gap-2">
										<span
											class="inline-flex h-7 min-w-7 items-center justify-center rounded-full border px-2 text-xs font-semibold tabular-nums {rankBadgeClass(
												r.run.entry.rank
											)}"
											aria-label={rankLabel(r.run.entry.rank)}>{r.run.entry.rank}</span
										>
										<span class="text-xs text-slate-600">of {r.run.total}</span>
									</span>
								{:else}
									<span class="text-slate-500">—</span>
								{/if}
							</td>
							<td class="px-4 py-2 text-slate-700" title={r.run ? absoluteTime(r.run.entry.achievedAt) : undefined}>
								{r.run ? relativeTime(r.run.entry.achievedAt, now) : '—'}
							</td>
							<td class="px-4 py-2 whitespace-nowrap text-right">
								{#if r.run}
									<a class="text-indigo-700 underline" href="/motor/watch/{r.run.entry.sessionId}">Watch</a>
									<span aria-hidden="true" class="text-slate-400"> · </span>
									<a class="text-indigo-700 underline" href={boardHref(r.map.id)}>Leaderboard</a>
								{:else}
									<button
										type="button"
										class="rounded-full bg-indigo-600 px-3 py-1 text-xs font-semibold text-white hover:bg-indigo-700 disabled:opacity-60"
										disabled={busy !== ''}
										onclick={() => play(r.map)}>{busy === r.map.id ? 'Creating…' : 'Play'}</button
									>
								{/if}
							</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
	{:else if !loading}
		<p class="rounded-xl border border-indigo-200 bg-white px-4 py-6 text-sm text-slate-600">
			{filter === 'solved' ? 'No maps solved yet.' : filter === 'unsolved' ? 'Every map is solved. Nicely done!' : 'No maps.'}
		</p>
	{/if}

	<p class="text-xs text-slate-600">
		Each map shows the player's best result on the “Fewest slots” board. Guest runs are remembered in this browser and kept on the server for
		a while after the last move. Sign in to keep your runs on a profile.
	</p>
</div>
