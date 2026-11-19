import { cleanup, render, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, it } from 'vitest';
import MiniGrid from './MiniGrid.svelte';

afterEach(cleanup);

/** Glyphs drawn inside the preview cells, in layout order (empty cells are skipped). */
const glyphs = () =>
	Array.from(document.querySelectorAll('[role="img"] div'))
		.map((cell) => cell.textContent?.trim() ?? '')
		.filter(Boolean);

describe('MiniGrid', () => {
	it('draws both ends of a portal pair, so a portal map is not a blank preview', () => {
		// Regression: the preview painted only floor/rock/treat/start, so portals (and every
		// other special tile) were invisible on the maps page, the home page and map cards.
		render(MiniGrid, {
			layout: ['>..#..1..*', '...#......', '...#......', '1..#......', '..........', '..........'],
			label: 'Preview of Portal Hop'
		});
		expect(glyphs()).toEqual(expect.arrayContaining(['▶', '1', '1']));
		expect(screen.getByRole('img').getAttribute('aria-label')).toBe(
			'Preview of Portal Hop. Tiles: 4 rocks, 1 treat, 2 portals'
		);
	});

	it('draws conveyors, mirrors, keys and doors with their own glyph and colour', () => {
		render(MiniGrid, { layout: ['>U/\\', 'aA..'] });
		expect(glyphs()).toEqual(expect.arrayContaining(['▲', '/', '\\', 'a', 'A']));
		const painted = Array.from(document.querySelectorAll('[role="img"] div'))
			.map((cell) => cell.getAttribute('style') ?? '')
			.join(' ');
		expect(painted).toContain('var(--board-conveyor)');
		expect(painted).toContain('var(--board-mirror)');
		// Key and door share one tone per letter, so a thumbnail shows the pairing.
		expect(painted).toContain('background: var(--board-lock-a); color: var(--board-lock-a-ink)');
		expect(painted).not.toContain('var(--board-key)');
		expect(screen.getByRole('img').getAttribute('aria-label')).toBe(
			'Map preview 4 by 2. Tiles: 1 conveyor, 2 mirrors, 1 key, 1 door'
		);
	});

	it('tints a second letter differently from the first', () => {
		render(MiniGrid, { layout: ['ab..', 'AB..'] });
		const painted = Array.from(document.querySelectorAll('[role="img"] div'))
			.map((cell) => cell.getAttribute('style') ?? '')
			.join(' ');
		expect(painted).toContain('var(--board-lock-a)');
		expect(painted).toContain('var(--board-lock-b)');
	});

	it('keeps the start arrow and says nothing about tiles a plain map lacks', () => {
		render(MiniGrid, { layout: ['>..*', '.#..'] });
		expect(glyphs()).toEqual(['▶']);
		expect(screen.getByRole('img').getAttribute('aria-label')).toBe('Map preview 4 by 2. Tiles: 1 rock, 1 treat');
	});

	it('names no tiles at all on an empty map', () => {
		render(MiniGrid, { layout: ['>...', '....'] });
		expect(screen.getByRole('img').getAttribute('aria-label')).toBe('Map preview 4 by 2');
	});
});
