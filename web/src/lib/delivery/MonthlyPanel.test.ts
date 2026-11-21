import { cleanup, render, screen } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import MonthlyPanel from './MonthlyPanel.svelte';
import type { MonthView } from './api';

beforeEach(() => localStorage.clear());
afterEach(cleanup);

const months: MonthView[] = [
	{
		index: 2,
		startTick: 600,
		endTick: 1199,
		income: 100,
		expenses: 40,
		net: 60,
		categories: [
			{ category: 'DELIVERY_INCOME', income: 100, expenses: 0 },
			{ category: 'CHARGING', income: 0, expenses: 40 }
		]
	},
	{
		index: 1,
		startTick: 0,
		endTick: 599,
		income: 0,
		expenses: 0,
		net: 0,
		categories: []
	}
];

describe('MonthlyPanel', () => {
	it('shows newest month first with net, totals and category rows', () => {
		render(MonthlyPanel, { months, ticksPerMonth: 600 });
		const rows = screen.getAllByTestId('month-row');
		expect(rows).toHaveLength(2);
		expect(rows[0].textContent).toContain('Month 2');
		expect(rows[0].textContent).toContain('t600–1199');
		expect(rows[0].textContent).toContain('+$100 in');
		expect(rows[0].textContent).toContain('−$40 out');
		expect(rows[0].textContent).toContain('Delivery income');
		expect(rows[0].textContent).toContain('+$100');
		expect(rows[0].textContent).toContain('Charging');
		expect(rows[0].textContent).toContain('−$40');
		expect(screen.getAllByTestId('month-net')[0].textContent).toBe('+$60');
	});

	it('renders a quiet month as a zero row instead of skipping it', () => {
		render(MonthlyPanel, { months, ticksPerMonth: 600 });
		const rows = screen.getAllByTestId('month-row');
		expect(rows[1].textContent).toContain('Month 1');
		expect(rows[1].textContent).toContain('No money moved this month.');
		expect(screen.getAllByTestId('month-net')[1].textContent).toBe('$0');
	});

	it('explains the month length and handles an empty report', () => {
		render(MonthlyPanel, { months: [], ticksPerMonth: 600 });
		expect(screen.getByText(/600 ticks each/)).toBeTruthy();
		expect(screen.getByText(/No months yet/)).toBeTruthy();
	});
});
