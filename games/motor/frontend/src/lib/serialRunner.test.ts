import { describe, expect, it } from 'vitest';
import { serialRunner } from './serialRunner';

const tick = () => new Promise<void>((r) => setTimeout(r, 0));

describe('serialRunner', () => {
	it('never runs two tasks at once and keeps call order', async () => {
		const log: string[] = [];
		let active = 0;
		let n = 0;
		const run = serialRunner(async () => {
			const id = ++n;
			active++;
			expect(active).toBe(1);
			log.push(`start ${id}`);
			await tick();
			await tick();
			log.push(`end ${id}`);
			active--;
		});
		await Promise.all([run(), run(), run()]);
		expect(log).toEqual(['start 1', 'end 1', 'start 2', 'end 2', 'start 3', 'end 3']);
	});

	it('a failed run rejects its own caller but does not block later runs', async () => {
		let n = 0;
		const run = serialRunner(async () => {
			if (++n === 1) throw new Error('boom');
		});
		const first = run();
		const second = run();
		await expect(first).rejects.toThrow('boom');
		await expect(second).resolves.toBeUndefined();
		expect(n).toBe(2);
	});
});
