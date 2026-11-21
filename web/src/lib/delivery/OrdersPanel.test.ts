import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import OrdersPanel from './OrdersPanel.svelte';
import type { OrderView } from './api';

beforeEach(() => localStorage.clear());
afterEach(cleanup);

const order = {
	id: 'o1',
	item: 'pizza',
	itemName: 'Pizza',
	quantity: 19,
	kitchenId: 'k1',
	kitchenName: 'Kitchen',
	customerId: 'c1',
	customerName: 'Customer',
	state: 'PENDING',
	assignedCar: null,
	createdTick: 0,
	deadlineTick: 100,
	ticksLeft: 40,
	reward: 400,
	paid: 0,
	late: false
} as unknown as OrderView;

describe('OrdersPanel vehicle fit', () => {
	it('blocks an order that does not fit the selected car and explains why', async () => {
		const onAssign = vi.fn();
		render(OrdersPanel, {
			orders: [order],
			selectedCarId: 'car_1',
			selectedCarCapacity: 4,
			onAssign
		});
		const button = screen.getByRole('button', { name: /Needs a 19-box vehicle/ });
		expect((button as HTMLButtonElement).disabled).toBe(true);
		expect(screen.getByText(/selected car holds 4 boxes/)).toBeTruthy();
		await fireEvent.click(button);
		expect(onAssign).not.toHaveBeenCalled();
	});

	it('offers Assign when the selected vehicle has room', () => {
		render(OrdersPanel, {
			orders: [order],
			selectedCarId: 'car_2',
			selectedCarCapacity: 24,
			onAssign: vi.fn()
		});
		expect((screen.getByRole('button', { name: 'Assign' }) as HTMLButtonElement).disabled).toBe(false);
	});
});
