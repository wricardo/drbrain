import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { activeDrag, DRAG_MIME, encodeDrag } from '../dnd';
import Palette from './Palette.svelte';

afterEach(() => {
	cleanup();
	activeDrag.set(null);
});

/** Minimal DataTransfer: jsdom doesn't implement one. */
function transfer(payload?: string) {
	const data = new Map<string, string>();
	if (payload) data.set(DRAG_MIME, payload);
	return {
		setData: (k: string, v: string) => void data.set(k, v),
		getData: (k: string) => data.get(k) ?? '',
		setDragImage: vi.fn(),
		effectAllowed: '',
		dropEffect: ''
	};
}

describe('Palette drag and drop', () => {
	it('publishes the active drag while a tile is dragged and clears it on dragend', async () => {
		render(Palette, { subCount: 1 });
		const tile = screen.getByRole('button', { name: /forward/i });
		const dt = transfer();
		await fireEvent.dragStart(tile, { dataTransfer: dt });
		let current: unknown;
		activeDrag.subscribe((v) => (current = v))();
		expect(current).toEqual({ kind: 'palette', instruction: 'MOVE_FORWARD' });
		expect(dt.setDragImage).toHaveBeenCalledOnce();
		await fireEvent.dragEnd(tile, { dataTransfer: dt });
		activeDrag.subscribe((v) => (current = v))();
		expect(current).toBeNull();
	});

	it('Erase accepts a dragged slot and reports it', async () => {
		const onerase = vi.fn();
		render(Palette, { subCount: 1, onerase });
		const slot = { kind: 'slot', tape: 0, slot: 1 } as const;
		activeDrag.set(slot);
		const erase = screen.getByTestId('palette-erase');
		const dt = transfer(encodeDrag(slot));
		await fireEvent.dragOver(erase, { dataTransfer: dt });
		expect(dt.dropEffect).toBe('move');
		expect(erase.className).toMatch(/outline-rose-500/); // armed as a drop target...
		expect(erase.className).toMatch(/ring-rose-300/); // ...and highlighted under the pointer
		expect(erase.getAttribute('title')).toMatch(/drop here to erase/i);
		await fireEvent.drop(erase, { dataTransfer: dt });
		expect(onerase).toHaveBeenCalledWith({ tape: 0, slot: 1 });
	});

	it('Erase ignores palette drags and disabled palettes', async () => {
		const onerase = vi.fn();
		const { rerender } = render(Palette, { subCount: 1, onerase });
		const palette = { kind: 'palette', instruction: 'TURN_LEFT' } as const;
		activeDrag.set(palette);
		await fireEvent.drop(screen.getByTestId('palette-erase'), { dataTransfer: transfer(encodeDrag(palette)) });
		expect(onerase).not.toHaveBeenCalled();

		const slot = { kind: 'slot', tape: -1, slot: 0 } as const;
		activeDrag.set(slot);
		await rerender({ subCount: 1, onerase, disabled: true });
		await fireEvent.drop(screen.getByTestId('palette-erase'), { dataTransfer: transfer(encodeDrag(slot)) });
		expect(onerase).not.toHaveBeenCalled();
	});

	it('erases a step dropped anywhere on the palette, not just on the Erase tile', async () => {
		const onerase = vi.fn();
		render(Palette, { subCount: 1, onerase });
		const slot = { kind: 'slot', tape: 1, slot: 2 } as const;
		activeDrag.set(slot);
		const dt = transfer(encodeDrag(slot));
		for (const target of [screen.getByRole('button', { name: /forward/i }), screen.getByTestId('palette')]) {
			onerase.mockClear();
			activeDrag.set(slot);
			await fireEvent.dragOver(target, { dataTransfer: dt });
			expect(dt.dropEffect).toBe('move');
			await fireEvent.drop(target, { dataTransfer: dt });
			expect(onerase).toHaveBeenCalledOnce();
			expect(onerase).toHaveBeenCalledWith({ tape: 1, slot: 2 });
		}
	});

	it('shows a drop affordance while a placed step is dragged, and hides it after', async () => {
		render(Palette, { subCount: 1 });
		const zone = screen.getByTestId('palette');
		expect(screen.queryByTestId('palette-erase-hint')).toBeNull();
		expect(zone.className).not.toMatch(/outline-rose-500/);

		const slot = { kind: 'slot', tape: -1, slot: 0 } as const;
		activeDrag.set(slot);
		expect(await screen.findByTestId('palette-erase-hint')).toBeTruthy();
		expect(zone.className).toMatch(/outline-rose-500/);

		await fireEvent.dragOver(zone, { dataTransfer: transfer(encodeDrag(slot)) });
		expect(zone.className).toMatch(/ring-rose-300/);

		await fireEvent.dragEnd(screen.getByTestId('palette-erase'), { dataTransfer: transfer() });
		expect(screen.queryByTestId('palette-erase-hint')).toBeNull();
	});

	it('dropping a palette tile on the palette stays a no-op with no affordance', async () => {
		const onerase = vi.fn();
		render(Palette, { subCount: 1, onerase });
		const tile = { kind: 'palette', instruction: 'MOVE_FORWARD' } as const;
		activeDrag.set(tile);
		const zone = screen.getByTestId('palette');
		const dt = transfer(encodeDrag(tile));
		await fireEvent.dragOver(zone, { dataTransfer: dt });
		expect(dt.dropEffect).toBe('');
		expect(screen.queryByTestId('palette-erase-hint')).toBeNull();
		await fireEvent.drop(screen.getByRole('button', { name: /forward/i }), { dataTransfer: dt });
		expect(onerase).not.toHaveBeenCalled();
	});

	it('a disabled palette shows no affordance and ignores drops anywhere', async () => {
		const onerase = vi.fn();
		render(Palette, { subCount: 1, onerase, disabled: true });
		const slot = { kind: 'slot', tape: 0, slot: 0 } as const;
		activeDrag.set(slot);
		expect(screen.queryByTestId('palette-erase-hint')).toBeNull();
		await fireEvent.drop(screen.getByTestId('palette'), { dataTransfer: transfer(encodeDrag(slot)) });
		expect(onerase).not.toHaveBeenCalled();
	});
});

describe('Palette stamping hint', () => {
	it('explains how to stop stamping and Escape disarms the tile', async () => {
		render(Palette, { subCount: 0, selected: 'MOVE_FORWARD' });
		expect(screen.getByTestId('palette-hint').textContent).toContain('Stamping Move forward');
		await fireEvent.keyDown(window, { key: 'Escape' });
		expect(screen.getByTestId('palette-hint').textContent).not.toContain('Stamping');
		expect(screen.getByRole('button', { name: /Move forward/ }).getAttribute('aria-pressed')).toBe('false');
	});
});
