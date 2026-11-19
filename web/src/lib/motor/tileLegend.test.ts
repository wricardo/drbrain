import { describe, expect, it } from 'vitest';
import { tileKindsOnMap } from './tileLegend';

const none = { conveyors: [], mirrors: [], keys: [], doors: [], portals: [] };

describe('tileKindsOnMap', () => {
	it('is empty for a plain map', () => {
		expect(tileKindsOnMap(none)).toEqual([]);
	});

	it('lists only the kinds the map uses, in a stable order', () => {
		const kinds = tileKindsOnMap({
			...none,
			portals: [{ pos: { x: 0, y: 0 }, pair: 1 }],
			conveyors: [{ pos: { x: 1, y: 0 }, dir: 'RIGHT' }]
		} as never);
		expect(kinds.map((k) => k.id)).toEqual(['conveyor', 'portal']);
	});

	it('keys and doors are separate entries, and a mirror covers both slants', () => {
		const kinds = tileKindsOnMap({
			...none,
			keys: [{ pos: { x: 0, y: 0 }, letter: 'a' }],
			doors: [{ pos: { x: 1, y: 0 }, letter: 'a' }],
			mirrors: [{ pos: { x: 2, y: 0 }, slash: true }]
		} as never);
		expect(kinds.map((k) => k.id)).toEqual(['mirror', 'key', 'door']);
	});

	it('tolerates a map from an older server without tile fields', () => {
		expect(tileKindsOnMap({} as never)).toEqual([]);
	});
});
