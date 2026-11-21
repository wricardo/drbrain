import { beforeAll, describe, expect, it } from 'vitest';
import {
	clearEngraveCache,
	decomposeRest,
	engraveMeasure,
	flipNotes,
	isDotted,
	measureParts,
	pitchKey,
	vexDuration
} from './engrave';
import type { Measure } from './types';

// jsdom has no canvas; VexFlow uses one only for text metrics. A tiny stub keeps
// the render path exercised without pulling the optional `canvas` package.
beforeAll(() => {
	Object.defineProperty(window.HTMLCanvasElement.prototype, 'getContext', {
		configurable: true,
		value: () => ({ measureText: (text: string) => ({ width: (text?.length ?? 1) * 6 }) })
	});
});

function fixture(): Measure {
	return {
		id: 'm001',
		len: 480,
		notes: [
			{ at: 0, dur: 120, pitch: 60 },
			{ at: 240, dur: 240, pitch: 64 },
			{ at: 240, dur: 60, pitch: 71 }
		]
	};
}

const canonical = (notes: { at: number; dur: number; pitch: number }[]) =>
	[...notes].sort((a, b) => a.at - b.at || a.pitch - b.pitch || a.dur - b.dur);

describe('tick -> VexFlow duration', () => {
	it('maps every authored note value', () => {
		const authored = [1920, 1440, 960, 720, 480, 360, 240, 180, 120, 60];
		expect(authored.map(vexDuration)).toEqual(['w', 'hd', 'h', 'qd', 'q', '8d', '8', '16d', '16', '32']);
	});

	it('marks exactly the dotted values', () => {
		expect([1440, 720, 360, 180].every(isDotted)).toBe(true);
		expect([1920, 960, 480, 240, 120, 60].some(isDotted)).toBe(false);
	});

	it('rejects tuplets and other unrenderable lengths', () => {
		expect(() => vexDuration(100)).toThrow(/renderable/);
		expect(() => vexDuration(320)).toThrow();
	});
});

describe('rest synthesis', () => {
	it('fills every gap so the parts sum to the measure length', () => {
		const parts = measureParts(
			{ id: 'r1', len: 480, notes: [{ at: 0, dur: 120, pitch: 60 }, { at: 240, dur: 120, pitch: 64 }] },
			{ h: false, v: false }
		);
		expect(parts.reduce((sum, p) => sum + p.dur, 0)).toBe(480);
		expect(parts.map((p) => p.at)).toEqual([0, 120, 240, 360]);
		expect(parts[0].pitches).toEqual([60]);
		expect(parts[1].pitches).toEqual([]);
		expect(parts[3].pitches).toEqual([]);
	});

	it('appends a trailing rest when the bar ends early', () => {
		const parts = measureParts({ id: 'r2', len: 480, notes: [{ at: 0, dur: 240, pitch: 60 }] }, { h: false, v: false });
		expect(parts).toEqual([
			{ at: 0, dur: 240, pitches: [60] },
			{ at: 240, dur: 240, pitches: [] }
		]);
	});

	it('decomposes gaps greedily on the 60-tick grid, dotted values included', () => {
		expect(decomposeRest(1440)).toEqual([1440]);
		expect(decomposeRest(720)).toEqual([720]);
		expect(decomposeRest(300)).toEqual([240, 60]);
		expect(decomposeRest(0)).toEqual([]);
		expect(() => decomposeRest(70)).toThrow();
	});
});

describe('flip math', () => {
	it('retrograde mirrors time exactly like the engine', () => {
		expect(flipNotes(fixture().notes, 480, { h: true, v: false })).toEqual([
			{ at: 0, dur: 240, pitch: 64 },
			{ at: 180, dur: 60, pitch: 71 },
			{ at: 360, dur: 120, pitch: 60 }
		]);
	});

	it('inversion mirrors pitch around B4 (71)', () => {
		expect(flipNotes(fixture().notes, 480, { h: false, v: true })).toEqual([
			{ at: 0, dur: 120, pitch: 82 },
			{ at: 240, dur: 60, pitch: 71 },
			{ at: 240, dur: 240, pitch: 78 }
		]);
	});

	it('both flips are involutions, alone and composed', () => {
		const m = fixture();
		const original = canonical(m.notes);
		for (const flip of [
			{ h: true, v: false },
			{ h: false, v: true },
			{ h: true, v: true }
		]) {
			expect(canonical(flipNotes(flipNotes(m.notes, m.len, flip), m.len, flip))).toEqual(original);
		}
	});

	it('spells pitches to match the key signature', () => {
		expect(pitchKey(60, 'C')).toBe('c/4');
		expect(pitchKey(61, 'D')).toBe('c#/4');
		expect(pitchKey(70, 'F')).toBe('bb/4');
	});
});

describe('engraveMeasure', () => {
	it('renders a measure to an SVG string with a stave and note glyphs', async () => {
		clearEngraveCache();
		const svg = await engraveMeasure(fixture(), { h: false, v: false }, { key: 'D', timeSig: '4/4', clef: 'treble' });
		expect(svg).toContain('<svg');
		expect(svg).toContain('vf-stave');
		expect(svg).toContain('<path');
		expect(svg).toContain('vf-stavenote');
	});

	it('caches by measure id and flip state', async () => {
		clearEngraveCache();
		const first = await engraveMeasure(fixture(), { h: true, v: true }, { key: 'C' });
		const second = await engraveMeasure(fixture(), { h: true, v: true }, { key: 'C' });
		expect(second).toBe(first);
	});

	it('renders rest-filled and dotted measures', async () => {
		clearEngraveCache();
		const measure: Measure = { id: 'm-rest', len: 1440, notes: [{ at: 0, dur: 1440, pitch: 67 }] };
		const svg = await engraveMeasure(measure, { h: false, v: false }, { key: 'G', timeSig: '3/4' });
		expect(svg).toContain('<svg');
	});

	// Regression: the card used to clip a 150px canvas at 104px, which hid every
	// notehead below the stave's middle. Assert the real glyph coordinates stay
	// inside the drawn area for both plain and inverted measures.
	it('keeps every notehead inside the stave box, plain and inverted', async () => {
		clearEngraveCache();
		const measure: Measure = {
			id: 'm-twinkle',
			len: 1920,
			notes: [
				{ at: 0, dur: 480, pitch: 60 },
				{ at: 480, dur: 480, pitch: 60 },
				{ at: 960, dur: 480, pitch: 67 },
				{ at: 1440, dur: 480, pitch: 67 }
			]
		};

		const inspect = (svg: string) => {
			const host = document.createElement('div');
			host.innerHTML = svg;
			const root = host.querySelector('svg');
			const heads = [...host.querySelectorAll('.vf-notehead text')].filter((text) => {
				const cp = text.textContent?.codePointAt(0) ?? 0;
				return cp >= 0xe0a2 && cp <= 0xe0a4;
			});
			const lines = [...host.querySelectorAll('.vf-stave path')].map((path) =>
				Number(/^M0 ([\d.]+)/.exec(path.getAttribute('d') ?? '')?.[1])
			);
			return {
				ys: heads.map((head) => Number(head.getAttribute('y'))),
				top: Math.min(...lines),
				bottom: Math.max(...lines),
				height: Number(root?.getAttribute('height'))
			};
		};

		for (const flip of [
			{ h: false, v: false },
			{ h: false, v: true }
		]) {
			const { ys, top, bottom, height } = inspect(
				await engraveMeasure(measure, flip, { key: 'C', timeSig: '4/4', clef: 'treble' })
			);
			expect(ys).toHaveLength(4);
			for (const y of ys) {
				expect(y).toBeGreaterThanOrEqual(top - 60);
				expect(y).toBeLessThanOrEqual(bottom + 60);
				expect(y).toBeGreaterThanOrEqual(0);
				expect(y).toBeLessThanOrEqual(height);
			}
		}
	});

	// Regression: inverted (and retrograde-inverted) measures put every note well
	// above the middle line, where a down stem must start at the head's centre,
	// attach at its left edge and run exactly one standard stem length. A stem
	// that straddles its head or doubles the length fails these bounds.
	it('anchors every stem to its head with one standard length, even inverted', async () => {
		clearEngraveCache();
		const measure: Measure = {
			id: 'm-inverted-stems',
			len: 1920,
			notes: [
				{ at: 0, dur: 480, pitch: 62 },
				{ at: 480, dur: 480, pitch: 62 },
				{ at: 960, dur: 960, pitch: 60 }
			]
		};

		for (const flip of [
			{ h: false, v: true },
			{ h: true, v: true }
		]) {
			const host = document.createElement('div');
			host.innerHTML = await engraveMeasure(measure, flip, { key: 'C', timeSig: '4/4', clef: 'treble' });
			const groups = [...host.querySelectorAll('.vf-stavenote')];
			expect(groups).toHaveLength(3);

			for (const group of groups) {
				const head = [...group.querySelectorAll('.vf-notehead text')].find((text) => {
					const cp = text.textContent?.codePointAt(0) ?? 0;
					return cp >= 0xe0a2 && cp <= 0xe0a4;
				});
				expect(head).toBeTruthy();
				const stem = group.querySelector('.vf-stem path');
				expect(stem).toBeTruthy();

				const path = /^M([-\d.]+) ([-\d.]+)L([-\d.]+) ([-\d.]+)$/.exec(stem?.getAttribute('d') ?? '');
				if (!path) throw new Error('stem path is not a single segment');
				const [stemX, stemY0, , stemY1] = path.slice(1).map(Number);
				const headX = Number(head?.getAttribute('x'));
				const headY = Number(head?.getAttribute('y'));

				// Starts at the head's vertical centre, one stem length away at the tip.
				expect(Math.abs(stemY0 - headY)).toBeLessThanOrEqual(3);
				const length = Math.abs(stemY1 - stemY0);
				expect(length).toBeGreaterThanOrEqual(30);
				expect(length).toBeLessThanOrEqual(55);

				// Attached to a head edge, not floating inside or outside the glyph.
				const offset = stemX - headX;
				expect(Math.abs(offset)).toBeGreaterThan(0.1);
				expect(Math.abs(offset)).toBeLessThan(13);
			}
		}
	});
});
