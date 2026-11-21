import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import { afterEach, beforeAll, describe, expect, it, vi } from 'vitest';
import MusicPuzzle from './MusicPuzzle.svelte';
import type { Measure, Session } from '../types';

// jsdom has no canvas; VexFlow uses one only for text metrics. Cards still
// render their interactive shell immediately, which is all this test needs.
beforeAll(() => {
	Object.defineProperty(window.HTMLCanvasElement.prototype, 'getContext', {
		configurable: true,
		value: () => ({ measureText: (text: string) => ({ width: (text?.length ?? 1) * 6 }) })
	});
});

afterEach(cleanup);

function measure(id: string, pitch: number): Measure {
	return { id, len: 480, notes: [{ at: 0, dur: 480, pitch }] };
}

function session(): Session {
	const measures = [measure('m1', 60), measure('m2', 64), measure('m3', 67)];
	return {
		id: 's1',
		displayName: null,
		difficulty: 'NOVICE',
		excerpt: {
			id: 'e1',
			title: 'Test Piece',
			composer: 'Anonymous',
			tempoBPM: 80,
			timeSig: '4/4',
			key: 'C',
			clef: 'treble',
			axis: 71,
			measures,
			notes: measures.flatMap((m) => m.notes.map((n) => ({ ...n })))
		},
		slots: measures.map((m, index) => ({ position: index, measureID: m.id, flipH: false, flipV: false })),
		solved: false,
		seq: 1,
		wins: 0,
		percentPerWin: 5,
		createdAt: '2026-11-20T00:00:00Z',
		lastActionAt: '2026-11-20T00:00:00Z',
		lastChange: null
	};
}

function setup(over: { playing?: boolean; busy?: boolean } = {}) {
	const handlers = {
		onplay: vi.fn(),
		onstop: vi.fn(),
		onswitch: vi.fn(),
		ontransform: vi.fn(),
		onreset: vi.fn()
	};
	render(MusicPuzzle, { session: session(), playing: false, busy: false, ...handlers, ...over });
	return handlers;
}

const disabled = (id: string) => (screen.getByTestId(id) as HTMLButtonElement).disabled;

describe('MusicPuzzle selection', () => {
	it('enables Switch only with exactly two measures selected', async () => {
		const h = setup();
		expect(disabled('switch')).toBe(true);

		await fireEvent.click(screen.getByTestId('measure-0'));
		expect(screen.getByTestId('measure-0').getAttribute('aria-pressed')).toBe('true');
		expect(disabled('switch')).toBe(true);

		await fireEvent.click(screen.getByTestId('measure-1'));
		expect(disabled('switch')).toBe(false);

		await fireEvent.click(screen.getByTestId('switch'));
		expect(h.onswitch).toHaveBeenCalledWith(0, 1);
	});

	it('deselects on a second tap and counts the selection', async () => {
		setup();
		await fireEvent.click(screen.getByTestId('measure-2'));
		expect(screen.getByTestId('selection-count').textContent).toContain('1');
		await fireEvent.click(screen.getByTestId('measure-2'));
		expect(screen.getByTestId('selection-count').textContent).toContain('0');
	});

	it('flips every selected measure and clears', async () => {
		const h = setup();
		expect(disabled('flip-h')).toBe(true);
		await fireEvent.click(screen.getByTestId('select-all'));
		expect(screen.getByTestId('selection-count').textContent).toContain('3');
		await fireEvent.click(screen.getByTestId('flip-h'));
		expect(h.ontransform).toHaveBeenCalledWith([0, 1, 2], 'h');
		await fireEvent.click(screen.getByTestId('clear'));
		expect(screen.getByTestId('selection-count').textContent).toContain('0');
	});

	it('shows Play and Stop from the playing prop', async () => {
		const h = setup({ playing: true });
		expect(screen.getByTestId('play').textContent).toContain('Stop');
		await fireEvent.click(screen.getByTestId('play'));
		expect(h.onstop).toHaveBeenCalledOnce();
	});
});
