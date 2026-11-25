<script lang="ts">
	import { onMount } from 'svelte';
	import { buildSeriesChart, type SeriesInput } from './series-chart';

	interface Props {
		series: SeriesInput[];
		/** Display names keyed by session id. */
		labels?: Record<string, string>;
		/** Formats y-axis labels and the legend values. */
		formatValue?: (value: number) => string;
		ariaLabel?: string;
		/** Chart height in px; defaults to a screen-size-aware value. */
		height?: number;
	}

	let {
		series,
		labels = {},
		formatValue = (value) => String(Math.round(value)),
		ariaLabel = 'Series by tick',
		height
	}: Props = $props();
	let wrap: HTMLDivElement | undefined = $state();
	let width = $state(960);

	onMount(() => {
		if (!wrap) return;
		const measure = () => {
			const next = Math.round(wrap?.clientWidth ?? 0);
			if (next >= 280) width = next;
		};
		measure();
		window.addEventListener('resize', measure);
		return () => window.removeEventListener('resize', measure);
	});

	const chart = $derived(
		buildSeriesChart(series, {
			width,
			height: height ?? (width < 640 ? 300 : 360),
			tickCount: width < 640 ? 3 : 5
		})
	);
	const hasData = $derived(chart.series.some((s) => s.points.length > 0));
	// A flat series still gets a padded domain, which can round two grid lines
	// to the same label ("1, 1, 0"). Keep the axis honest by showing each
	// formatted label once.
	const yTicks = $derived.by(() => {
		const seen = new Set<string>();
		return chart.yTicks.filter((tick) => {
			const label = formatValue(tick.value);
			if (seen.has(label)) return false;
			seen.add(label);
			return true;
		});
	});
</script>

{#if !hasData}
	<p class="py-8 text-center text-sm text-muted">No data in this window.</p>
{:else}
	<div class="w-full" bind:this={wrap}>
		<svg
			class="h-auto w-full"
			viewBox="0 0 {chart.width} {chart.height}"
			role="img"
			aria-label={ariaLabel}
			data-testid="series-chart"
		>
			<g aria-hidden="true">
				{#each yTicks as tick (tick.value)}
					<line
						class="grid"
						x1={chart.plot.left}
						x2={chart.plot.left + chart.plot.width}
						y1={tick.y}
						y2={tick.y}
					/>
					<text class="axis" x={chart.plot.left - 8} y={tick.y} text-anchor="end" dominant-baseline="middle">
						{formatValue(tick.value)}
					</text>
				{/each}
				{#each chart.xTicks as tick, i (tick.value)}
					<line
						class="grid faint"
						x1={tick.x}
						x2={tick.x}
						y1={chart.plot.top}
						y2={chart.plot.top + chart.plot.height}
					/>
					<text
						class="axis"
						x={tick.x}
						y={tick.y + 20}
						text-anchor={i === 0 ? 'start' : i === chart.xTicks.length - 1 ? 'end' : 'middle'}
					>
						{tick.label}
					</text>
				{/each}
			</g>

			{#each chart.series as s (s.sessionId)}
				{#if s.dot}
					<circle class="dot series-{s.colorIndex}" cx={s.dot.x} cy={s.dot.y} r="3.5" />
				{:else if s.path}
					<path class="line series-{s.colorIndex}" d={s.path} fill="none" />
				{/if}
			{/each}
		</svg>

		<ul class="mt-3 flex flex-wrap gap-x-5 gap-y-2 text-xs" aria-label="Sessions in the chart">
			{#each chart.series as s (s.sessionId)}
				<li class="flex items-center gap-2" data-testid="series-legend">
					<span class="swatch series-{s.colorIndex} inline-block h-2.5 w-2.5 rounded-full" aria-hidden="true"
					></span>
					<span class="max-w-56 truncate">{labels[s.sessionId] ?? s.sessionId}</span>
					<span class="text-faint tabular-nums">{formatValue(s.last?.value ?? 0)}</span>
				</li>
			{/each}
		</ul>
	</div>
{/if}

<style>
	.grid {
		stroke: var(--db-line);
		stroke-width: 1;
	}
	.grid.faint {
		opacity: 0.55;
	}
	.axis {
		fill: var(--db-muted);
		font-size: 12px;
	}
	.line {
		stroke: currentColor;
		stroke-width: 2.5;
		stroke-linecap: round;
		stroke-linejoin: round;
		vector-effect: non-scaling-stroke;
	}
	.dot {
		fill: currentColor;
	}
	.swatch {
		background: currentColor;
	}
	.series-0 {
		color: #234d9e;
	}
	.series-1 {
		color: #0e7490;
	}
	.series-2 {
		color: #92400e;
	}
	.series-3 {
		color: #6d28d9;
	}
	.series-4 {
		color: #9d174d;
	}
	.series-5 {
		color: #146444;
	}
	:global(.dark) .series-0 {
		color: #9db4ef;
	}
	:global(.dark) .series-1 {
		color: #7dd3fc;
	}
	:global(.dark) .series-2 {
		color: #fbbf24;
	}
	:global(.dark) .series-3 {
		color: #c4b5fd;
	}
	:global(.dark) .series-4 {
		color: #f9a8d4;
	}
	:global(.dark) .series-5 {
		color: #86efac;
	}
</style>
