import { describe, expect, it } from 'vitest';
import {
	MAX_MAP_ID,
	blankCells,
	copyId,
	formatLayout,
	localLayoutIssues,
	parseLayout,
	resizeCells
} from './layout';

describe('layout', () => {
	const layout = ['.*.', '#.<', '...'];

	it('parses tiles, start glyph/facing and counts', () => {
		const p = parseLayout(layout);
		expect(p.width).toBe(3);
		expect(p.height).toBe(3);
		expect(p.start).toEqual({ x: 2, y: 1, facing: 'LEFT' });
		expect(p.startCount).toBe(1);
		expect(p.treatCount).toBe(1);
		expect(p.cells[1][0]).toBe('#');
		expect(p.cells[1][2]).toBe('.');
	});

	it('round-trips through format', () => {
		const p = parseLayout(layout);
		expect(formatLayout(p.cells, p.start)).toEqual(layout);
	});

	it('uses y-down rows: row 0 is the top', () => {
		expect(parseLayout(['.....', '.....', '..^..']).start).toEqual({ x: 2, y: 2, facing: 'UP' });
	});

	it('resizes keeping content and padding with empty', () => {
		const p = parseLayout(layout);
		const bigger = resizeCells(p.cells, 4, 4);
		expect(bigger[0]).toEqual(['.', '*', '.', '.']);
		expect(bigger[3]).toEqual(['.', '.', '.', '.']);
		expect(resizeCells(p.cells, 2, 2)).toEqual([
			['.', '*'],
			['#', '.']
		]);
	});

	it('preserves every tile glyph instead of flattening it to empty', () => {
		const tiles = ['>UDLR*', '.abc..', '/\\AB1.'];
		const p = parseLayout(tiles);
		expect(p.cells[0].slice(1, 5)).toEqual(['U', 'D', 'L', 'R']);
		expect(p.cells[1].slice(1, 4)).toEqual(['a', 'b', 'c']);
		expect(p.cells[2]).toEqual(['/', '\\', 'A', 'B', '1', '.']);
		expect(p.keyLetters).toEqual(['a', 'b', 'c']);
		expect(p.doorLetters).toEqual(['A', 'B']);
		expect(p.portalDigits).toEqual(['1']);
	});

	it('round-trips the new glyphs through format', () => {
		const tiles = ['>UDLR*', '.abc..', '/\\AB1.'];
		const p = parseLayout(tiles);
		expect(formatLayout(p.cells, p.start)).toEqual(tiles);
	});

	it('still reads the start arrows and turns unknown glyphs into empty', () => {
		const p = parseLayout(['>?v<', '....', '.*..']);
		expect(p.start).toEqual({ x: 0, y: 0, facing: 'RIGHT' });
		expect(p.startCount).toBe(3);
		expect(p.cells[0]).toEqual(['.', '.', '.', '.']);
	});

	it('flags a door without its key and a key without its door', () => {
		expect(localLayoutIssues(parseLayout(['>..*', '..A.', '....']))).toContain(
			"Door 'A' has no key 'a' on this map."
		);
		expect(localLayoutIssues(parseLayout(['>..*', '..a.', '....']))).toContain(
			"Key 'a' has no door 'A' on this map."
		);
		expect(localLayoutIssues(parseLayout(['>..*', '..aA', '....']))).toEqual([]);
	});

	it('flags a portal digit that does not appear exactly twice', () => {
		expect(localLayoutIssues(parseLayout(['>1.*', '..1.', '....']))).toEqual([]);
		expect(localLayoutIssues(parseLayout(['>1.*', '....', '....']))).toContain(
			"Portal '1' must appear exactly twice (found 1)."
		);
		expect(localLayoutIssues(parseLayout(['>1.*', '.11.', '....']))).toContain(
			"Portal '1' must appear exactly twice (found 3)."
		);
	});

	it('reports local issues: start count, treats, size', () => {
		expect(localLayoutIssues(parseLayout(layout))).toEqual([]);
		const none = parseLayout(formatLayout(blankCells(3, 3), null));
		expect(localLayoutIssues(none)).toEqual(['Place exactly one start cell.', 'Place at least one treat.']);
		const two = parseLayout(['^.>', '.*.', '...']);
		expect(localLayoutIssues(two)).toContain('Place exactly one start cell.');
		expect(localLayoutIssues(parseLayout(['^*', '..']))[0]).toMatch(/per side/);
	});
});

describe('copyId', () => {
	it('appends _copy for ids that leave room', () => {
		expect(copyId('comb')).toBe('comb_copy');
		expect(copyId('a')).toBe('a_copy');
	});

	it('never exceeds the server id limit', () => {
		const long = 'z'.repeat(MAX_MAP_ID);
		const out = copyId(long);
		expect(out.length).toBeLessThanOrEqual(MAX_MAP_ID);
		expect(out).toMatch(/^[a-z0-9_-]{1,64}$/);
	});

	it('does not leave a trailing separator after truncating', () => {
		const id = `${'a'.repeat(MAX_MAP_ID - 6)}-`;
		const out = copyId(id);
		expect(out).toMatch(/^[a-z0-9_-]{1,64}$/);
		expect(out).not.toMatch(/[-_]_copy$/);
	});
});
