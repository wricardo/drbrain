import { describe, expect, it } from 'vitest';
import { SPEED_PRESETS, clampSpeed, presetFor } from './speed';

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
