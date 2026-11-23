import { cleanup, render, screen } from '@testing-library/svelte';
import { afterEach, expect, it } from 'vitest';
import Map from './Map.svelte';
import type { CarView, CityView, OrderView } from './api';

afterEach(cleanup);

it('shows carrying before assigned on a shared tile and clears the marker when neither applies', async () => {
	const city = { gridSize: 2, rows: ['1.', '..'], kitchens: [], customers: [], hiddenTiles: 0 } as unknown as CityView;
	const empty = { id: 'one', name: 'One', className: 'van', x: 0, y: 0, state: 'IDLE', cargo: [] } as unknown as CarView;
	const loaded = { id: 'two', name: 'Two', className: 'van', x: 0, y: 0, state: 'CHARGING', cargo: ['order'] } as unknown as CarView;
	const assignedCarIds = new Set(['one']);
	const { rerender } = render(Map, { city, cars: [empty, loaded], orders: [], assignedCarIds, selectedCarId: null });

	const marker = screen.getAllByRole('button', { name: '2 cars at 0,0, One (assigned to an order), Two (carrying an order)' }).find((button) => button.tabIndex === 0)!;
	expect(marker.querySelector('.bg-amber-300')).not.toBeNull();
	await rerender({ city, cars: [empty, { ...loaded, cargo: [] }], orders: [], assignedCarIds, selectedCarId: null });
	const assignedMarker = screen.getAllByRole('button', { name: '2 cars at 0,0, One (assigned to an order), Two' }).find((button) => button.tabIndex === 0)!;
	expect(assignedMarker.querySelector('.bg-sky-300')).not.toBeNull();
	await rerender({ city, cars: [empty, { ...loaded, cargo: [] }], orders: [], assignedCarIds: new Set(), selectedCarId: null });
	const emptyMarker = screen.getAllByRole('button', { name: '2 cars at 0,0, One, Two' }).find((button) => button.tabIndex === 0)!;
	expect(emptyMarker.querySelector('.bg-amber-300, .bg-sky-300')).toBeNull();
});

it('marks both endpoints of active orders and clears them after completion', async () => {
	const city = {
		gridSize: 3, rows: ['KH?', '1..', '...'], hiddenTiles: 1,
		kitchens: [{ id: 'k', x: 0, y: 0 }, { id: 'hidden', x: 2, y: 0 }, { id: 'covered', x: 0, y: 1 }],
		customers: [{ id: 'c', x: 1, y: 0 }]
	} as unknown as CityView;
	const car = { id: 'one', name: 'One', className: 'van', x: 0, y: 1, state: 'IDLE', cargo: [] } as unknown as CarView;
	const assigned = { id: 'a', state: 'ASSIGNED', kitchenId: 'k', customerId: 'c' } as OrderView;
	const picked = { ...assigned, id: 'b', state: 'PICKED_UP' } as OrderView;
	const hidden = { ...assigned, id: 'h', kitchenId: 'hidden' } as OrderView;
	const covered = { ...assigned, id: 'v', kitchenId: 'covered' } as OrderView;
	const props = { city, cars: [car], assignedCarIds: new Set<string>(), selectedCarId: null };
	const { rerender } = render(Map, { ...props, orders: [assigned, picked, hidden, covered] });

	expect(screen.getByLabelText('Kitchen — pickup at 0,0, order picked up')).toBeTruthy();
	expect(screen.getByLabelText('Customer — waiting for a delivery at 1,0, order picked up')).toBeTruthy();
	expect(screen.getByLabelText('Unexplored — impassable at 2,0')).toBeTruthy();
	expect(screen.getByRole('button', { name: '1 car at 0,1, One; location: assigned, awaiting pickup' })).toBeTruthy();

	await rerender({ ...props, orders: [assigned, { ...picked, state: 'DONE' }, hidden] });
	expect(screen.getByLabelText('Kitchen — pickup at 0,0, assigned, awaiting pickup')).toBeTruthy();
	expect(screen.getByLabelText('Customer — waiting for a delivery at 1,0, assigned, awaiting pickup')).toBeTruthy();

	await rerender({ ...props, orders: [{ ...assigned, state: 'EXPIRED' }, { ...picked, state: 'DONE' }] });
	expect(screen.getByLabelText('Kitchen — pickup at 0,0')).toBeTruthy();
	expect(screen.getByLabelText('Customer — waiting for a delivery at 1,0')).toBeTruthy();
});

it('shows assigned fleet numbers until pickup at kitchens and delivery at customers', async () => {
	const city = {
		gridSize: 3, rows: ['KH.', '...', '...'], hiddenTiles: 0,
		kitchens: [{ id: 'k', x: 0, y: 0 }], customers: [{ id: 'c', x: 1, y: 0 }]
	} as unknown as CityView;
	const cars = [
		{ id: 'one', name: 'One', className: 'van', x: 0, y: 1, state: 'IDLE', cargo: [] },
		{ id: 'two', name: 'Two', className: 'van', x: 1, y: 1, state: 'IDLE', cargo: [] }
	] as unknown as CarView[];
	const one = { id: 'a', state: 'ASSIGNED', assignedCar: 'one', kitchenId: 'k', customerId: 'c' } as OrderView;
	const two = { ...one, id: 'b', assignedCar: 'two' } as OrderView;
	const props = { city, cars, assignedCarIds: new Set(['one', 'two']), selectedCarId: null };
	const { rerender } = render(Map, { ...props, orders: [two, one] });
	expect(screen.getByLabelText('Kitchen — pickup at 0,0, assigned, awaiting pickup, fleet 1, 2')).toBeTruthy();
	expect(screen.getByLabelText('Customer — waiting for a delivery at 1,0, assigned, awaiting pickup, fleet 1, 2')).toBeTruthy();
	expect(screen.getAllByText('1,2')).toHaveLength(2);

	await rerender({ ...props, orders: [{ ...one, state: 'PICKED_UP' }, two] });
	expect(screen.getByLabelText('Kitchen — pickup at 0,0, order picked up, fleet 2')).toBeTruthy();
	expect(screen.getByLabelText('Customer — waiting for a delivery at 1,0, order picked up, fleet 1, 2')).toBeTruthy();

	await rerender({ ...props, orders: [{ ...one, state: 'DONE' }, { ...two, state: 'PICKED_UP' }] });
	expect(screen.getByLabelText('Kitchen — pickup at 0,0, order picked up')).toBeTruthy();
	expect(screen.getByLabelText('Customer — waiting for a delivery at 1,0, order picked up, fleet 2')).toBeTruthy();

	await rerender({ ...props, orders: [{ ...one, state: 'DONE' }, { ...two, state: 'DONE' }] });
	expect(screen.getByLabelText('Kitchen — pickup at 0,0')).toBeTruthy();
	expect(screen.getByLabelText('Customer — waiting for a delivery at 1,0')).toBeTruthy();
});

it('marks unassigned customers red, even when another order is in transit', async () => {
	const city = {
		gridSize: 3, rows: ['KH?', '1..', '...'], hiddenTiles: 1,
		kitchens: [{ id: 'k', x: 0, y: 0 }],
		customers: [{ id: 'c', x: 1, y: 0 }, { id: 'hidden', x: 2, y: 0 }, { id: 'covered', x: 0, y: 1 }]
	} as unknown as CityView;
	const car = { id: 'one', name: 'One', className: 'van', x: 0, y: 1, state: 'IDLE', cargo: [] } as unknown as CarView;
	const pending = { id: 'a', state: 'PENDING', assignedCar: null, kitchenId: 'k', customerId: 'c' } as OrderView;
	const hidden = { ...pending, id: 'h', customerId: 'hidden' };
	const covered = { ...pending, id: 'v', customerId: 'covered' };
	const props = { city, cars: [car], assignedCarIds: new Set<string>(), selectedCarId: null };
	const { container, rerender } = render(Map, { ...props, orders: [pending, hidden, covered] });
	const dots = () => container.querySelectorAll('div[aria-hidden="true"].z-30 > span.bg-red-500');
	expect(screen.getByLabelText('Customer — waiting for a delivery at 1,0, unassigned order')).toBeTruthy();
	expect(screen.getByLabelText('Kitchen — pickup at 0,0')).toBeTruthy();
	expect(screen.getByLabelText('Unexplored — impassable at 2,0')).toBeTruthy();
	expect(screen.getByRole('button', { name: '1 car at 0,1, One; location: unassigned order' })).toBeTruthy();
	expect(dots()).toHaveLength(2);

	const picked = { ...pending, id: 'b', state: 'PICKED_UP', assignedCar: 'one' } as OrderView;
	await rerender({ ...props, orders: [picked, pending] });
	expect(screen.getByLabelText('Customer — waiting for a delivery at 1,0, order picked up, unassigned order, fleet 1')).toBeTruthy();
	expect(dots()).toHaveLength(1);

	await rerender({ ...props, orders: [{ ...pending, state: 'ASSIGNED', assignedCar: 'one' }] });
	expect(screen.getByLabelText('Customer — waiting for a delivery at 1,0, assigned, awaiting pickup, fleet 1')).toBeTruthy();
	expect(dots()).toHaveLength(0);
});
