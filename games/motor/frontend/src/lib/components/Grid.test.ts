import { cleanup, render, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, it } from 'vitest';
import Grid from './Grid.svelte';
import type { GameMap, VMState } from '$lib/types';

afterEach(cleanup);

const map = (over: Partial<GameMap> = {}): GameMap => ({
	id: 'tiles',
	name: 'Tiles',
	description: '',
	difficulty: 'easy',
	builtin: false,
	width: 4,
	height: 2,
	layout: ['>U/*', '....'],
	start: { x: 0, y: 0 },
	startFacing: 'RIGHT',
	rocks: [],
	treats: [{ x: 3, y: 0 }],
	conveyors: [{ pos: { x: 1, y: 0 }, dir: 'UP' }],
	mirrors: [{ pos: { x: 2, y: 0 }, slash: true }],
	keys: [{ pos: { x: 0, y: 1 }, letter: 'a' }],
	doors: [{ pos: { x: 1, y: 1 }, letter: 'A' }],
	portals: [
		{ pos: { x: 2, y: 1 }, pair: '1' },
		{ pos: { x: 3, y: 1 }, pair: '1' }
	],
	mainTapeLength: 3,
	subTapeLengths: [],
	maxSteps: 50,
	maxCallDepth: 4,
	allowRecursion: false,
	...over
});

const vm = (over: Partial<VMState> = {}): VMState => ({
	pos: { x: 0, y: 0 },
	facing: 'RIGHT',
	treatsRemaining: [{ x: 3, y: 0 }],
	callStack: [{ tape: -1, pc: 0 }],
	steps: 0,
	status: 'READY',
	lossReason: null,
	visited: [],
	openDoors: [],
	...over
});

describe('Grid tiles', () => {
	it('names the tiles present in the aria-label', () => {
		render(Grid, { map: map() });
		const board = screen.getByTestId('board');
		expect(board.getAttribute('role')).toBe('img');
		expect(board.getAttribute('aria-label')).toMatch(/1 conveyor/);
		expect(board.getAttribute('aria-label')).toMatch(/1 mirror/);
		expect(board.getAttribute('aria-label')).toMatch(/1 key/);
		expect(board.getAttribute('aria-label')).toMatch(/1 door/);
		expect(board.getAttribute('aria-label')).toMatch(/2 portals/);
	});

	it('says nothing about tiles on a plain map', () => {
		render(Grid, {
			map: map({ conveyors: [], mirrors: [], keys: [], doors: [], portals: [], layout: ['>..*', '....'] })
		});
		expect(screen.getByTestId('board').getAttribute('aria-label')).not.toContain('Tiles:');
	});

	it('draws a locked door and an open one with different state on the glyph', () => {
		const { unmount } = render(Grid, { map: map(), vm: vm() });
		const locked = document.querySelector('[data-testid="door"]');
		expect(locked?.getAttribute('data-state')).toBe('locked');
		expect(locked?.getAttribute('data-letter')).toBe('A');
		// A padlock badge is drawn only while the door is shut.
		expect(locked?.querySelector('rect[fill="var(--board-lock-body)"]')).not.toBeNull();
		unmount();
		render(Grid, { map: map(), vm: vm({ openDoors: ['a'] }) });
		const open = document.querySelector('[data-testid="door"]');
		expect(open?.getAttribute('data-state')).toBe('open');
		expect(open?.querySelector('rect[fill="var(--board-lock-body)"]')).toBeNull();
		// The doorway becomes a recess with the leaf swung out, so an open door reads as a way through.
		expect(open?.querySelector('rect[fill="var(--board-door-recess)"]')).not.toBeNull();
		expect(open?.querySelector('path[fill="var(--board-lock-a)"]')).not.toBeNull();
	});

	it('paints a key and its door in the same tone, and dims a spent key', () => {
		const { unmount } = render(Grid, { map: map(), vm: vm() });
		const key = document.querySelector('[data-testid="key"]');
		const leaf = document.querySelector('[data-testid="door"] rect');
		expect(key?.innerHTML).toContain('var(--board-lock-a)');
		expect(leaf?.getAttribute('fill')).toBe('var(--board-lock-a)');
		expect(key?.getAttribute('data-spent')).toBe('false');
		unmount();
		render(Grid, { map: map(), vm: vm({ openDoors: ['a'] }) });
		expect(document.querySelector('[data-testid="key"]')?.getAttribute('data-spent')).toBe('true');
	});

	it('names the key/door pairing in the board label', () => {
		render(Grid, {
			map: map({ keys: [{ pos: { x: 0, y: 1 }, letter: 'a' }, { pos: { x: 3, y: 1 }, letter: 'b' }], doors: [{ pos: { x: 1, y: 1 }, letter: 'A' }, { pos: { x: 2, y: 1 }, letter: 'B' }] }),
			vm: vm({ openDoors: ['a'] })
		});
		const label = screen.getByTestId('board').getAttribute('aria-label') ?? '';
		expect(label).toContain('Keys: a opens A, b opens B; open: A.');
	});

	it('draws a conveyor chevron, a mirror diagonal, the key letter and the portal digits', () => {
		render(Grid, { map: map() });
		const texts = Array.from(document.querySelectorAll('svg text')).map((t) => t.textContent);
		expect(texts).toEqual(expect.arrayContaining(['a', 'A', '1', '1']));
		expect(document.querySelectorAll('svg polygon').length).toBeGreaterThan(0);
		expect(document.querySelectorAll('svg line').length).toBe(1);
	});

	it('keeps a portal digit inside its own cell, so a top-row pair is never clipped', () => {
		// Regression: the digit used to sit above the tile, outside the viewBox on row 0.
		for (const y of [0, 1]) {
			const { unmount } = render(Grid, {
				map: map({
					portals: [{ pos: { x: 2, y }, pair: '1' }],
					layout: map().layout
				})
			});
			const text = Array.from(document.querySelectorAll('svg text')).find((t) => t.textContent === '1');
			const digitY = Number(text?.getAttribute('y'));
			expect(digitY).toBeGreaterThanOrEqual(y * 40);
			expect(digitY).toBeLessThanOrEqual((y + 1) * 40);
			unmount();
		}
	});
});
