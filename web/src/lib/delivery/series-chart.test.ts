import { cleanup, render, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, it } from 'vitest';
import SeriesChart from './SeriesChart.svelte';
import { buildSeriesChart, type SeriesInput } from './series-chart';
import { formatCount, formatMoney, formatReputation } from './analytics-format';

afterEach(cleanup);

const series: SeriesInput[] = [
	{
		sessionId: 'alpha',
		cityId: 'city',
		points: [
			{ tick: 0, value: 0 },
			{ tick: 100, value: 150 },
			{ tick: 200, value: 90 }
		]
	},
	{
		sessionId: 'beta',
		cityId: 'saopaulo',
		points: [
			{ tick: 0, value: 10 },
			{ tick: 250, value: 300 }
		]
	}
];

describe('buildSeriesChart', () => {
	it('spans every session on one tick axis and includes the zero baseline', () => {
		const chart = buildSeriesChart(series);
		expect(chart.series).toHaveLength(2);
		expect(chart.xTicks[0].value).toBe(0);
		expect(chart.xTicks.at(-1)?.value).toBe(250);
		expect(chart.yTicks.some((tick) => tick.value === 0)).toBe(true);
		expect(chart.xTicks[0].x).toBeLessThan(chart.xTicks.at(-1)!.x);
		expect(chart.series[0].path).toMatch(/^M/);
		expect(chart.series[0].last?.value).toBe(90);
		expect(chart.series[1].last?.value).toBe(300);
	});

	it('marks a single point for a visible dot', () => {
		const chart = buildSeriesChart([
			{ sessionId: 'one', cityId: 'city', points: [{ tick: 0, value: 0 }] }
		]);
		expect(chart.series[0].dot).not.toBeNull();
	});

	it('formats money, counts and reputation', () => {
		expect(formatMoney(1885)).toBe('$1,885');
		expect(formatCount(3984)).toBe('3,984');
		expect(formatReputation(4.9)).toBe('4.90');
	});
});

describe('SeriesChart', () => {
	it('draws one path per session and names them in the legend', () => {
		render(SeriesChart, {
			series,
			labels: { alpha: 'Alpha city', beta: 'Beta city' },
			formatValue: formatCount
		});
		expect(screen.getByTestId('series-chart')).toBeTruthy();
		expect(screen.getAllByTestId('series-legend')).toHaveLength(2);
		expect(screen.getByText('Alpha city')).toBeTruthy();
		expect(screen.getByText('Beta city')).toBeTruthy();
		expect(screen.getAllByText('300').length).toBeGreaterThan(0);
	});

	it('explains an empty window instead of drawing axes', () => {
		render(SeriesChart, { series: [] });
		expect(screen.getByText('No data in this window.')).toBeTruthy();
		expect(screen.queryByTestId('series-chart')).toBeNull();
	});
});
