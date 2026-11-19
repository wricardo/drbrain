import { afterEach, describe, expect, it } from 'vitest';
import { SPEED_PRESETS, clampSpeed, initialSpeed, presetFor, saveSpeed, savedSpeed } from './speed';

describe('speed presets', () => {
	it('are ordered slowest to fastest and stay inside the server range', () => {
		const ms = SPEED_PRESETS.map((p) => p.ms);
		expect(ms).toEqual([...ms].sort((a, b) => b - a));
		expect(ms.every((v) => v >= 50 && v <= 5000)).toBe(true);
	});

	it('presetFor matches exactly and returns null for custom speeds', () => {
		expect(presetFor(500)?.id).toBe('normal');
		expect(presetFor(50)?.id).toBe('turbo');
		expect(presetFor(123)).toBeNull();
	});

	it('clampSpeed keeps values within 50-5000', () => {
		expect(clampSpeed(10)).toBe(50);
		expect(clampSpeed(9000)).toBe(5000);
		expect(clampSpeed(750)).toBe(750);
	});
});

describe('remembered speed', () => {
	afterEach(() => localStorage.clear());

	it('saves and reads the chosen speed, clamped, and ignores junk', () => {
		expect(savedSpeed()).toBeNull();
		saveSpeed(200);
		expect(savedSpeed()).toBe(200);
		saveSpeed(99999);
		expect(savedSpeed()).toBe(5000);
		localStorage.setItem('drbrain-motor-programming.speedMs', 'fast');
		expect(savedSpeed()).toBeNull();
	});

	it('a fresh session opens at the saved speed, a used one keeps its own', () => {
		const fresh = { speedMs: 500, attempts: 0, vm: { steps: 0 } };
		expect(initialSpeed(fresh, 200)).toBe(200);
		expect(initialSpeed(fresh, null)).toBe(500);
		expect(initialSpeed({ speedMs: 1000, attempts: 2, vm: { steps: 0 } }, 200)).toBe(1000);
		expect(initialSpeed({ speedMs: 1000, attempts: 0, vm: { steps: 3 } }, 200)).toBe(1000);
		expect(initialSpeed({ speedMs: 0, attempts: 1, vm: { steps: 0 } }, null)).toBe(500);
	});
});
