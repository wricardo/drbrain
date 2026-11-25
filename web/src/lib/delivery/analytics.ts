import type { AnalyticsMetric, SessionSummary, ValueSeries } from './api';
import { formatCount, formatMoney } from './analytics-format';

/**
 * The label used in the analytics picker and chart legend: the player's own
 * session name first, then ownership, then the owner's name, then the id.
 */
export function sessionLabel(
	s: Pick<SessionSummary, 'id' | 'displayName' | 'ownerDisplayName' | 'mine'>
): string {
	if (s.displayName) return s.displayName;
	if (s.mine) return 'Your city';
	if (s.ownerDisplayName && s.ownerDisplayName !== 'Guest') {
		return `${s.ownerDisplayName}’s city`;
	}
	return `City …${s.id.slice(-6)}`;
}

/**
 * Pick a useful starting overlay: sessions that have progressed, capped at
 * limit. Fall back to the newest saves when every session is still at tick 0.
 */
export function defaultSelection(saves: SessionSummary[], limit = 5): string[] {
	const progressed = saves.filter((s) => s.tick > 0 || s.money > 0);
	const pool = progressed.length > 0 ? progressed : saves;
	return pool.slice(0, limit).map((s) => s.id);
}

/** One chart in the cumulative operational metric set. */
export interface MetricChart {
	metric: AnalyticsMetric;
	id: string;
	title: string;
	description: string;
	money?: boolean;
}

/** Balance and delivered-volume series, shown first on the play screen. */
export const OVERVIEW_CHARTS: MetricChart[] = [
	{
		metric: 'MONEY',
		id: 'money',
		title: 'Cash by tick',
		description: 'Balance after every checking-account movement.',
		money: true
	},
	{
		metric: 'BOXES_DELIVERED',
		id: 'boxes-delivered',
		title: 'Items delivered by tick',
		description: 'Cumulative boxes from done orders; expired and open orders do not count.'
	},
	{
		metric: 'REPUTATION',
		id: 'reputation',
		title: 'Reputation by tick',
		description: 'Recorded standing, scaled 0–5.'
	}
];

/** Order-count series, shown together on the analytics screens. */
export const ORDER_CHARTS: MetricChart[] = [
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

/** Money-flow series, shown together on the analytics screens. */
export const MONEY_CHARTS: MetricChart[] = [
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
		description: 'Cumulative fines actually charged for expired orders, as spend.',
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

/** Empty metric set, so a component can render before its first fetch. */
export function emptyMetrics(): Record<AnalyticsMetric, ValueSeries[]> {
	return {
		MONEY: [],
		BOXES_DELIVERED: [],
		REPUTATION: [],
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

/** Sum of the last point across every session series. */
export function latestSeriesTotal(series: ValueSeries[]): number {
	return series.reduce((sum, h) => sum + (h.points[h.points.length - 1]?.value ?? 0), 0);
}

/** Format a chart value using the metric's own unit. */
export function formatMetric(chart: MetricChart, value: number): string {
	return chart.money ? formatMoney(value) : formatCount(value);
}
