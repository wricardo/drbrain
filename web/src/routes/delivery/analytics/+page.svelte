<script lang="ts">
	import { onMount } from 'svelte';
	import {
		api,
		ApiError,
		type AnalyticsMetric,
		type MoneyHistory,
		type SessionSummary,
		type ValueSeries
	} from '$lib/delivery/api';
	import { defaultSelection, sessionLabel } from '$lib/delivery/analytics';
	import { formatCount, formatMoney, formatReputation } from '$lib/delivery/analytics-format';
	import type { SeriesInput } from '$lib/delivery/series-chart';
	import { pageTitle } from '$lib/delivery/brand';
	import SeriesChart from '$lib/delivery/SeriesChart.svelte';
	import Button from '@drbrain/ui/components/Button.svelte';
	import EmptyState from '@drbrain/ui/components/EmptyState.svelte';
	import Skeleton from '@drbrain/ui/components/Skeleton.svelte';

	const MAX_SELECTED = 6;

	interface MetricChart {
		metric: AnalyticsMetric;
		id: string;
		title: string;
		description: string;
		money?: boolean;
	}

	const ORDER_CHARTS: MetricChart[] = [
		{
			metric: 'DELIVERIES',
			id: 'deliveries',
			title: 'Deliveries by tick',
			description: 'Cumulative on-time orders completed, counted at the delivery tick.'
		},
		{
			metric: 'LATE_DELIVERIES',
			id: 'late-deliveries',
			title: 'Late deliveries by tick',
			description: 'Cumulative orders completed after their deadline.'
		},
		{
			metric: 'EXPIRED_ORDERS',
			id: 'expired-orders',
			title: 'Expired orders by tick',
			description: 'Cumulative orders that expired undelivered, counted just after the deadline.'
		},
		{
			metric: 'ORDERS_CREATED',
			id: 'orders-created',
			title: 'Orders raised by tick',
			description: 'Every customer order ever raised, including later expired ones.'
		}
	];

	const MONEY_CHARTS: MetricChart[] = [
		{
			metric: 'DELIVERY_INCOME',
			id: 'delivery-income',
			title: 'Delivery income by tick',
			description: 'Cumulative cash paid by completed orders, on time and late.',
			money: true
		},
		{
			metric: 'ORDER_FINES',
			id: 'order-fines',
			title: 'Expired-order fines by tick',
			description: 'Cumulative fines charged for expired orders, as spend.',
			money: true
		},
		{
			metric: 'UPKEEP_SPEND',
			id: 'upkeep-spend',
			title: 'Upkeep spend by tick',
			description: 'Cumulative fleet running cost paid, as spend.',
			money: true
		},
		{
			metric: 'CHARGING_SPEND',
			id: 'charging-spend',
			title: 'Charging spend by tick',
			description: 'Cumulative paid charging on chargers and depots, as spend.',
			money: true
		},
		{
			metric: 'TOW_SPEND',
			id: 'tow-spend',
			title: 'Tow spend by tick',
			description: 'Cumulative tow fees paid, as spend.',
			money: true
		}
	];

	function emptyMetrics(): Record<AnalyticsMetric, ValueSeries[]> {
		return {
			DELIVERIES: [],
			LATE_DELIVERIES: [],
			EXPIRED_ORDERS: [],
			ORDERS_CREATED: [],
			DELIVERY_INCOME: [],
			ORDER_FINES: [],
			UPKEEP_SPEND: [],
			CHARGING_SPEND: [],
			TOW_SPEND: []
		};
	}

	let saves = $state<SessionSummary[]>([]);
	let loading = $state(true);
	let failed = $state(false);
	let error = $state<string | null>(null);

	let selected = $state<string[]>([]);
	let money = $state<SeriesInput[]>([]);
	let items = $state<SeriesInput[]>([]);
	let reputation = $state<SeriesInput[]>([]);
	let metrics = $state<Record<AnalyticsMetric, ValueSeries[]>>(emptyMetrics());
	let chartLoading = $state(false);
	let chartError = $state<string | null>(null);
	let requestSeq = 0;

	const labels = $derived(Object.fromEntries(saves.map((s) => [s.id, sessionLabel(s)])));
	const metricsEmpty = $derived(Object.values(metrics).every((rows) => rows.length === 0));
	const latestCash = $derived(
		money.reduce((sum, h) => sum + (h.points[h.points.length - 1]?.value ?? 0), 0)
	);
	const latestItems = $derived(
		items.reduce((sum, h) => sum + (h.points[h.points.length - 1]?.value ?? 0), 0)
	);

	function latestTotal(series: ValueSeries[]): number {
		return series.reduce((sum, h) => sum + (h.points[h.points.length - 1]?.value ?? 0), 0);
	}

	function formatMetric(chart: MetricChart, value: number): string {
		return chart.money ? formatMoney(value) : formatCount(value);
	}

	function message(e: unknown): string {
		return e instanceof ApiError ? `${e.message} (${e.code})` : String(e);
	}

	function moneySeries(rows: MoneyHistory[]): SeriesInput[] {
		return rows.map((h) => ({
			sessionId: h.sessionId,
			cityId: h.cityId,
			points: h.points.map((p) => ({ tick: p.tick, value: p.money }))
		}));
	}

	async function loadSeries() {
		const ids = selected.slice();
		if (ids.length === 0) {
			money = [];
			items = [];
			reputation = [];
			metrics = emptyMetrics();
			chartError = null;
			chartLoading = false;
			return;
		}
		const seq = ++requestSeq;
		chartLoading = true;
		chartError = null;
		try {
			const [moneyRows, itemRows, reputationRows, metricRows] = await Promise.all([
				api.moneyHistory(ids, { maxPoints: 500 }),
				api.itemsHistory(ids, { maxPoints: 500 }),
				api.reputationHistory(ids, { maxPoints: 500 }),
				api.metricsHistory(ids, { maxPoints: 500 })
			]);
			if (seq !== requestSeq) return;
			money = moneySeries(moneyRows);
			items = itemRows;
			reputation = reputationRows;
			metrics = metricRows;
		} catch (e) {
			if (seq !== requestSeq) return;
			money = [];
			items = [];
			reputation = [];
			metrics = emptyMetrics();
			chartError = message(e);
		} finally {
			if (seq === requestSeq) chartLoading = false;
		}
	}

	async function loadSaves() {
		loading = true;
		failed = false;
		error = null;
		try {
			saves = await api.sessions();
			selected = defaultSelection(saves, MAX_SELECTED);
			await loadSeries();
		} catch (e) {
			failed = true;
			error = message(e);
		} finally {
			loading = false;
		}
	}

	function toggle(id: string) {
		if (selected.includes(id)) {
			selected = selected.filter((s) => s !== id);
		} else if (selected.length < MAX_SELECTED) {
			selected = [...selected, id];
		}
		void loadSeries();
	}

	onMount(() => {
		void loadSaves();
	});
</script>

<svelte:head>
	<title>{pageTitle('Analytics')}</title>
</svelte:head>

<div class="db-container py-8">
	<header class="mb-6 flex flex-wrap items-end justify-between gap-4">
		<div>
			<p class="db-kicker">Delivery</p>
			<h1 class="text-3xl font-bold tracking-tight">Analytics by tick</h1>
			<p class="mt-1 max-w-2xl text-sm text-muted">
				Compare cash, boxes delivered, reputation, orders and spend on the same game clock.
				Tick 0 is each session’s start, so sessions played at different times line up.
			</p>
		</div>
		<Button variant="secondary" href="/delivery">Back to play</Button>
	</header>

	{#if error}
		<p
			class="mb-4 rounded-lg border border-[var(--db-danger)]/40 bg-[var(--db-danger)]/10 px-4 py-3 text-sm text-[var(--db-danger)]"
			role="alert"
		>
			{error}
		</p>
	{/if}

	{#if loading}
		<div class="grid gap-4 lg:grid-cols-[minmax(0,20rem)_minmax(0,1fr)]">
			<Skeleton label="Loading saves…" />
			<Skeleton label="Loading analytics…" />
		</div>
	{:else if failed}
		<EmptyState title="Could not load analytics" description="Try loading the saves again.">
			<Button variant="secondary" onclick={loadSaves}>Retry</Button>
		</EmptyState>
	{:else if saves.length === 0}
		<EmptyState
			title="No saved cities yet"
			description="Start a city and drive a delivery. Its history appears here after it saves."
		>
			<Button href="/delivery">Start a city</Button>
		</EmptyState>
	{:else}
		<div class="grid items-start gap-6 lg:grid-cols-[minmax(0,20rem)_minmax(0,1fr)]">
			<section class="db-panel p-4" aria-labelledby="analytics-sessions">
				<div class="mb-2 flex items-baseline justify-between gap-2">
					<h2 id="analytics-sessions" class="text-sm font-bold">Sessions</h2>
					<span class="text-xs text-faint">{selected.length}/{MAX_SELECTED}</span>
				</div>
				<p class="mb-3 text-xs text-muted">
					Pick up to {MAX_SELECTED}. Each chart spans every selected session from tick 0 to
					its latest saved tick.
				</p>
				<ul class="max-h-96 divide-y divide-edge overflow-y-auto">
					{#each saves as s (s.id)}
						{@const checked = selected.includes(s.id)}
						<li>
							<label
								class="flex min-h-11 cursor-pointer items-center gap-3 px-1 py-2 hover:bg-surface-muted"
							>
								<input
									type="checkbox"
									class="h-4 w-4 shrink-0 accent-[var(--db-accent)]"
									{checked}
									disabled={!checked && selected.length >= MAX_SELECTED}
									onchange={() => toggle(s.id)}
								/>
								<span class="min-w-0 flex-1">
									<span class="block truncate text-sm">{labels[s.id]}</span>
									<span class="block truncate text-xs text-faint">
										tick {s.tick.toLocaleString('en-US')} · {formatMoney(s.money)}
										{#if s.loaded}· live{/if}
									</span>
								</span>
							</label>
						</li>
					{/each}
				</ul>
			</section>

			<div class="grid min-w-0 gap-6">
				{#if chartError}
					<p
						class="rounded-lg border border-[var(--db-danger)]/40 bg-[var(--db-danger)]/10 px-4 py-3 text-sm text-[var(--db-danger)]"
						role="alert"
					>
						{chartError}
					</p>
				{:else if selected.length === 0}
					<EmptyState
						title="Select a session"
						description="Pick at least one session to draw its history."
					/>
				{:else if chartLoading && money.length === 0 && items.length === 0 && reputation.length === 0 && metricsEmpty}
					<Skeleton label="Loading analytics…" />
				{:else}
					<section class="db-panel min-w-0 p-4 sm:p-6" aria-labelledby="chart-money">
						<div class="mb-3">
							<h2 id="chart-money" class="text-sm font-bold">Cash by tick</h2>
							<p class="mt-0.5 text-xs text-muted">
								Every movement of the balance. Sessions: {money.length} · latest cash in
								view: {formatMoney(latestCash)}.
							</p>
						</div>
						<SeriesChart series={money} {labels} formatValue={formatMoney} ariaLabel="Cash by tick" />
					</section>

					<section class="db-panel min-w-0 p-4 sm:p-6" aria-labelledby="chart-items">
						<div class="mb-3">
							<h2 id="chart-items" class="text-sm font-bold">Items delivered by tick</h2>
							<p class="mt-0.5 text-xs text-muted">
								Cumulative boxes from done orders; expired and open orders do not count.
								Boxes in view: {formatCount(latestItems)}.
							</p>
						</div>
						<SeriesChart
							series={items}
							{labels}
							formatValue={formatCount}
							ariaLabel="Items delivered by tick"
						/>
					</section>

					<section class="db-panel min-w-0 p-4 sm:p-6" aria-labelledby="chart-reputation">
						<div class="mb-3">
							<h2 id="chart-reputation" class="text-sm font-bold">Reputation by tick</h2>
							<p class="mt-0.5 text-xs text-muted">
								Recorded standing, scaled 0–5. Sessions saved before the reputation ledger
								existed start at their current recorded value.
							</p>
						</div>
						<SeriesChart
							series={reputation}
							{labels}
							formatValue={formatReputation}
							ariaLabel="Reputation by tick"
						/>
					</section>

					<section aria-labelledby="charts-orders">
						<h2 id="charts-orders" class="text-lg font-bold">Orders by tick</h2>
						<p class="mb-4 mt-1 text-sm text-muted">
							Cumulative counts from the indexed order history. Expired and open orders
							never count as deliveries.
						</p>
						<div class="grid gap-6 xl:grid-cols-2">
							{#each ORDER_CHARTS as chart (chart.metric)}
								{@const series = metrics[chart.metric] ?? []}
								<section
									class="db-panel min-w-0 p-4 sm:p-5"
									aria-labelledby={`chart-${chart.id}`}
								>
									<div class="mb-3">
										<h3 id={`chart-${chart.id}`} class="text-sm font-bold">{chart.title}</h3>
										<p class="mt-0.5 text-xs text-muted">
											{chart.description} Latest in view:
											{formatMetric(chart, latestTotal(series))}.
										</p>
									</div>
									<SeriesChart
										{series}
										{labels}
										formatValue={(value) => formatMetric(chart, value)}
										ariaLabel={chart.title}
									/>
								</section>
							{/each}
						</div>
					</section>

					<section aria-labelledby="charts-money-flow">
						<h2 id="charts-money-flow" class="text-lg font-bold">Money flow by tick</h2>
						<p class="mb-4 mt-1 text-sm text-muted">
							Cumulative cash movement by category. Spend is plotted as a positive
							magnitude so income and costs share one scale.
						</p>
						<div class="grid gap-6 xl:grid-cols-2">
							{#each MONEY_CHARTS as chart (chart.metric)}
								{@const series = metrics[chart.metric] ?? []}
								<section
									class="db-panel min-w-0 p-4 sm:p-5"
									aria-labelledby={`chart-${chart.id}`}
								>
									<div class="mb-3">
										<h3 id={`chart-${chart.id}`} class="text-sm font-bold">{chart.title}</h3>
										<p class="mt-0.5 text-xs text-muted">
											{chart.description} Latest in view:
											{formatMetric(chart, latestTotal(series))}.
										</p>
									</div>
									<SeriesChart
										{series}
										{labels}
										formatValue={(value) => formatMetric(chart, value)}
										ariaLabel={chart.title}
									/>
								</section>
							{/each}
						</div>
					</section>
				{/if}
			</div>
		</div>
	{/if}
</div>

{#if !loading && saves.length > 0}
	<p class="db-container pb-8 text-xs text-faint">
		Charts read the indexed delivery projection, not live sessions. History can lag a running
		session by one autosave interval; deleting a session deletes its projection.
	</p>
{/if}
