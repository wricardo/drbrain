import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import RunControls from './RunControls.svelte';

afterEach(cleanup);

function setup(over: Record<string, unknown> = {}) {
	const handlers = { onrun: vi.fn(), onpause: vi.fn(), onstep: vi.fn(), onreset: vi.fn() };
	render(RunControls, { playing: false, finished: false, canReset: false, speedMs: 500, ...handlers, ...over });
	return handlers;
}

const disabled = (id: string) => (screen.getByTestId(id) as HTMLButtonElement).disabled;

describe('RunControls', () => {
	it('idle: Run is the primary action, Step works, Reset waits until there is something to reset', async () => {
		const h = setup();
		expect(screen.queryByTestId('pause')).toBeNull();
		expect(disabled('reset')).toBe(true);
		await fireEvent.click(screen.getByTestId('run'));
		await fireEvent.click(screen.getByTestId('step'));
		expect(h.onrun).toHaveBeenCalledOnce();
		expect(h.onstep).toHaveBeenCalledOnce();
	});

	it('Reset is enabled once the robot has moved', async () => {
		const h = setup({ canReset: true });
		expect(disabled('reset')).toBe(false);
		await fireEvent.click(screen.getByTestId('reset'));
		expect(h.onreset).toHaveBeenCalledOnce();
	});

	it('while running the primary button becomes Pause and Step is locked', async () => {
		const h = setup({ playing: true, canReset: true });
		expect(screen.queryByTestId('run')).toBeNull();
		expect(disabled('step')).toBe(true);
		await fireEvent.click(screen.getByTestId('pause'));
		expect(h.onpause).toHaveBeenCalledOnce();
	});

	it('after a win or loss the primary button becomes Try again (reset) and Step is locked', async () => {
		const h = setup({ finished: true, canReset: true });
		expect(screen.queryByTestId('run')).toBeNull();
		expect(disabled('step')).toBe(true);
		await fireEvent.click(screen.getByTestId('retry'));
		expect(h.onreset).toHaveBeenCalledOnce();
		expect(h.onrun).not.toHaveBeenCalled();
	});

	it('busy disables every action', () => {
		setup({ busy: true, canReset: true });
		expect(disabled('run')).toBe(true);
		expect(disabled('step')).toBe(true);
		expect(disabled('reset')).toBe(true);
	});

	it('speed presets reflect and change the speed; custom speeds are shown', async () => {
		setup({ speedMs: 500 });
		expect(screen.getByTestId('speed-normal').getAttribute('aria-checked')).toBe('true');
		expect(screen.getByTestId('speed-fast').getAttribute('aria-checked')).toBe('false');
		await fireEvent.click(screen.getByTestId('speed-fast'));
		expect(screen.getByTestId('speed-fast').getAttribute('aria-checked')).toBe('true');
		expect(screen.getByTestId('speed-normal').getAttribute('aria-checked')).toBe('false');

		cleanup();
		setup({ speedMs: 123 });
		expect(screen.getByTestId('custom-speed').textContent).toContain('123 ms');
		expect(screen.getByTestId('speed-normal').getAttribute('aria-checked')).toBe('false');
	});

	it('speed is locked while running', () => {
		setup({ playing: true });
		expect((screen.getByTestId('speed-fast') as HTMLButtonElement).disabled).toBe(true);
	});
});

describe('RunControls with an empty Main tape', () => {
	it('keeps Run and Step off so an attempt is not wasted', () => {
		setup({ empty: true });
		expect(disabled('run')).toBe(true);
		expect(disabled('step')).toBe(true);
	});

	it('still offers Pause while running and Try again after a finished run', () => {
		setup({ empty: true, playing: true });
		expect(disabled('pause')).toBe(false);
		cleanup();
		setup({ empty: true, finished: true });
		expect(disabled('retry')).toBe(false);
	});
});
