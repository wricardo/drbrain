import { describe, expect, it } from 'vitest';
import type { SessionSummary } from './api';
import { defaultSelection, sessionLabel } from './analytics';

function save(id: string, overrides: Partial<SessionSummary> = {}): SessionSummary {
	return {
		id,
		createdAt: '2026-01-01T00:00:00Z',
		tick: 0,
		speed: 0,
		money: 0,
		reputation: 3,
		deliveries: 0,
		loaded: false,
		ownerDisplayName: 'Guest',
		ownerHandle: null,
		mine: false,
		...overrides
	} as SessionSummary;
}

describe('analytics labels', () => {
	it('names your city, a known owner, and a guest', () => {
		expect(sessionLabel(save('aaaaaaaaaaaa', { mine: true }))).toBe('Your city');
		expect(sessionLabel(save('aaaaaaaaaaaa', { ownerDisplayName: 'Ada' }))).toBe('Ada’s city');
		expect(sessionLabel(save('123456789abc'))).toBe('City …789abc');
	});

	it('prefers progressed sessions and caps the default overlay', () => {
		const saves = [
			save('empty-1'),
			save('played-1', { tick: 40, money: 100 }),
			save('played-2', { tick: 10, money: 5 }),
			save('played-3', { tick: 20 }),
			save('played-4', { tick: 30 }),
			save('played-5', { tick: 50 }),
			save('played-6', { tick: 60 })
		];
		expect(defaultSelection(saves, 3)).toEqual(['played-1', 'played-2', 'played-3']);
	});

	it('falls back to the newest saves when everything is at tick 0', () => {
		expect(defaultSelection([save('a'), save('b')], 1)).toEqual(['a']);
	});
});
