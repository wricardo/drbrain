<script lang="ts">
	import { api, ApiError, type ValueSeries } from './api';
	import {
		emptyMetrics,
		formatMetric,
		latestSeriesTotal,
		MONEY_CHARTS,
		ORDER_CHARTS,
		OVERVIEW_CHARTS
	} from './analytics';
	import SeriesChart from './SeriesChart.svelte';
	import Skeleton from '@drbrain/ui/components/Skeleton.svelte';

	/**
	 * Cumulative analytics for the session being played. Unlike the analytics
	 * tab this is one city only, so the charts read as that city's own trend.
	 * History comes from the indexed projection, which the autosave refreshes,
	 * so a new fetch every AUTOSAVE_TICKS keeps it close without querying on
	 * every frame.
	 */
	interface Props {
		sessionId: string;
		tick?: number;
	}

	const AUTOSAVE_TICKS = 60;

	let { sessionId, tick = 0 }: Props = $props();

	let metrics = $state(emptyMetrics());
	let loading = $state(true);
	let error = $state<string | null>(null);
	let loadedId = $state('');
	let loadedTick = $state(-1);
	let requestSeq = 0;

	const labels = $derived({ [sessionId]: 'This city' });
	const hasData = $derived(Object.values(metrics).some((rows) => rows.length > 0));

	function message(e: unknown): string {
		return e instanceof ApiError ? `${e.message} (${e.code})` : String(e);
	}

	async function load(id: string, atTick: number) {
		const seq = ++requestSeq;
		loading = true;
		error = null;
		try {
			const rows = await api.metricsHistory([id], { maxPoints: 300 });
			if (seq !== requestSeq) return;
			metrics = rows;
			loadedId = id;
			loadedTick = atTick;
		} catch (e) {
			if (seq !== requestSeq) return;
			error = message(e);
		} finally {
			if (seq === requestSeq) loading = false;
		}
	}

	$effect(() => {
		if (!sessionId) return;
		const changed = sessionId !== loadedId;
		if (!changed && tick - loadedTick < AUTOSAVE_TICKS) return;
		void load(sessionId, tick);
	});
</script>

{#if error}
	<p
		class="m-3 rounded-lg border border-[var(--db-danger)]/40 bg-[var(--db-danger)]/10 px-3 py-2 text-xs text-[var(--db-danger)]"
		role="alert"
	>
		{error}
		<button type="button" class="ml-1 underline" onclick={() => load(sessionId, tick)}>Retry</button>
	</p>
{:else if loading && !hasData}
	<div class="p-3">
		<Skeleton label="Loading analytics…" />
	</div>
{:else if !hasData}
	<p class="p-3 text-sm text-muted">
		No saved history yet. Charts appear after the city autosaves, about every 60 ticks.
	</p>
{:else}
	<div class="space-y-4 p-3">
		{#each [{ title: 'Overview', charts: OVERVIEW_CHARTS }, { title: 'Orders', charts: ORDER_CHARTS }, { title: 'Money flow', charts: MONEY_CHARTS }] as group (group.title)}
			<section aria-label={`${group.title} by tick`}>
				<h3 class="mb-2 text-xs font-semibold tracking-wide text-muted uppercase">
					{group.title}
				</h3>
				<div class="grid gap-4 md:grid-cols-2">
					{#each group.charts as chart (chart.metric)}
						{@const series = metrics[chart.metric] ?? []}
						<div class="min-w-0">
							<p class="text-xs font-semibold text-body">{chart.title}</p>
							<p class="mb-1 text-xs text-muted">
								{chart.description} Latest: {formatMetric(chart, latestSeriesTotal(series))}.
							</p>
							<SeriesChart
								{series}
								{labels}
								formatValue={(value: number) => formatMetric(chart, value)}
								ariaLabel={`${chart.title} for this city`}
								height={220}
							/>
						</div>
					{/each}
				</div>
			</section>
		{/each}
		<p class="text-xs text-faint">
			History comes from the autosave projection, so the newest ~60 ticks can lag.
		</p>
	</div>
{/if}
