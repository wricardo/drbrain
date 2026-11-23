export interface SeriesPointInput {
	tick: number;
	value: number;
}

export interface SeriesInput {
	sessionId: string;
	cityId: string;
	points: SeriesPointInput[];
}

export interface ChartPoint {
	tick: number;
	value: number;
}

export interface ChartTick {
	value: number;
	x: number;
	y: number;
	label: string;
}

export interface ChartSeries {
	sessionId: string;
	cityId: string;
	points: ChartPoint[];
	path: string;
	/** Set when the series has one point; the component draws a dot. */
	dot: { x: number; y: number } | null;
	last: ChartPoint | null;
	colorIndex: number;
}

export interface SeriesChartModel {
	width: number;
	height: number;
	plot: { left: number; top: number; width: number; height: number };
	xTicks: ChartTick[];
	yTicks: ChartTick[];
	series: ChartSeries[];
}

export interface SeriesChartOptions {
	width?: number;
	height?: number;
	/** Number of labelled grid lines per axis. */
	tickCount?: number;
}

const compactFormat = new Intl.NumberFormat('en-US', {
	notation: 'compact',
	maximumFractionDigits: 1
});

function ticks(min: number, max: number, count: number): number[] {
	const n = Math.max(2, Math.floor(count));
	if (min === max) return [min];
	const step = (max - min) / (n - 1);
	return Array.from({ length: n }, (_, i) => min + step * i);
}

/**
 * Build the SVG geometry for a tick-indexed overlay.
 *
 * The y axis always includes zero: hiding the baseline exaggerates small
 * movements. The x axis spans every selected session, so sessions played at
 * different wall-clock times share one tick scale.
 */
export function buildSeriesChart(
	series: SeriesInput[],
	options: SeriesChartOptions = {}
): SeriesChartModel {
	const width = options.width ?? 960;
	const height = options.height ?? 360;
	const tickCount = options.tickCount ?? 5;
	const left = 68;
	const right = 16;
	const top = 16;
	const bottom = 36;
	const plot = { left, top, width: width - left - right, height: height - top - bottom };

	let minTick = 0;
	let maxTick = 0;
	let minValue = 0;
	let maxValue = 0;
	for (const s of series) {
		for (const p of s.points) {
			if (!Number.isFinite(p.tick) || !Number.isFinite(p.value)) continue;
			minTick = Math.min(minTick, p.tick);
			maxTick = Math.max(maxTick, p.tick);
			minValue = Math.min(minValue, p.value);
			maxValue = Math.max(maxValue, p.value);
		}
	}
	if (maxTick === minTick) maxTick = minTick + 1;
	if (maxValue === minValue) maxValue = minValue + Math.max(1, Math.abs(minValue) * 0.1);

	const x = (tick: number) => plot.left + ((tick - minTick) / (maxTick - minTick)) * plot.width;
	const y = (value: number) =>
		plot.top + plot.height - ((value - minValue) / (maxValue - minValue)) * plot.height;

	const xTicks = ticks(minTick, maxTick, tickCount).map((value) => ({
		value,
		x: x(value),
		y: plot.top + plot.height,
		label: compactFormat.format(Math.round(value))
	}));
	const yTicks = ticks(minValue, maxValue, tickCount).map((value) => ({
		value,
		x: plot.left,
		y: y(value),
		label: ''
	}));

	const chartSeries = series.map((s, index) => {
		const points = s.points.filter((p) => Number.isFinite(p.tick) && Number.isFinite(p.value));
		const path =
			points.length === 1
				? `M${x(points[0].tick).toFixed(2)} ${y(points[0].value).toFixed(2)} h0.01`
				: points
						.map((p, i) => `${i === 0 ? 'M' : 'L'}${x(p.tick).toFixed(2)} ${y(p.value).toFixed(2)}`)
						.join(' ');
		return {
			sessionId: s.sessionId,
			cityId: s.cityId,
			points,
			path,
			dot:
				points.length === 1
					? { x: x(points[0].tick), y: y(points[0].value) }
					: null,
			last: points.length > 0 ? points[points.length - 1] : null,
			colorIndex: index % 6
		};
	});

	return { width, height, plot, xTicks, yTicks, series: chartSeries };
}
