import { describe, expect, it, vi } from 'vitest';
import { replayTo, stepBackTarget } from './stepBack';

const snap = (steps: number, status = 'RUNNING') => ({ steps, vm: { status } });

describe('stepBackTarget', () => {
	it('is one fewer step, and null at the start', () => {
		expect(stepBackTarget(5)).toBe(4);
		expect(stepBackTarget(1)).toBe(0);
		expect(stepBackTarget(0)).toBeNull();
	});
});

describe('replayTo', () => {
	it('resets, then steps the requested number of times and returns the last snapshot', async () => {
		let n = 0;
		const reset = vi.fn(async () => snap((n = 0), 'READY'));
		const step = vi.fn(async () => snap(++n));
		const s = await replayTo(3, reset, step);
		expect(reset).toHaveBeenCalledOnce();
		expect(step).toHaveBeenCalledTimes(3);
		expect(s.steps).toBe(3);
	});

	it('only resets when going back to the start', async () => {
		const step = vi.fn(async () => snap(1));
		const s = await replayTo(0, async () => snap(0, 'READY'), step);
		expect(step).not.toHaveBeenCalled();
		expect(s.steps).toBe(0);
	});

	it('stops if the run ends early instead of stepping a finished run', async () => {
		let n = 0;
		const step = vi.fn(async () => snap(++n, n === 2 ? 'WON' : 'RUNNING'));
		const s = await replayTo(5, async () => snap(0, 'READY'), step);
		expect(step).toHaveBeenCalledTimes(2);
		expect(s.vm.status).toBe('WON');
	});

	it('passes a failure through', async () => {
		await expect(replayTo(2, async () => snap(0, 'READY'), async () => Promise.reject(new Error('boom')))).rejects.toThrow('boom');
	});
});
