import { describe, expect, it } from 'vitest';
import { MAX_MAP_ID, blankCells, copyId, formatLayout, localLayoutIssues, parseLayout, resizeCells } from './layout';

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
