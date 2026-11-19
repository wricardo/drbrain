import type { Facing, Position } from './types';

/**
 * Every glyph a layout can carry. The server treats unknown glyphs as empty, so
 * parseLayout keeps this list as the source of truth for what round-trips.
 * Conveyors ('U','D','L','R') carry one extra cell, '/' and '\\' mirror the
 * facing, 'a'..'c' are keys, 'A'..'C' their doors and '1'..'9' portal pairs.
 */
export type Tile =
	| '.'
	| '#'
	| '*'
	| 'U'
	| 'D'
	| 'L'
	| 'R'
	| '/'
	| '\\'
	| 'a'
	| 'b'
	| 'c'
	| 'A'
	| 'B'
	| 'C'
	| '1'
	| '2'
	| '3'
	| '4'
	| '5'
	| '6'
	| '7'
	| '8'
	| '9';

/** Tunnel/plain tiles, in the order the editor groups them. */
export const CONVEYOR_GLYPHS = ['U', 'D', 'L', 'R'] as const;
export const MIRROR_GLYPHS = ['/', '\\'] as const;
export const KEY_GLYPHS = ['a', 'b', 'c'] as const;
export const DOOR_GLYPHS = ['A', 'B', 'C'] as const;
export const PORTAL_GLYPHS = ['1', '2', '3', '4', '5', '6', '7', '8', '9'] as const;

const TILE_GLYPHS: Record<string, true> = Object.fromEntries(
	['.', '#', '*', ...CONVEYOR_GLYPHS, ...MIRROR_GLYPHS, ...KEY_GLYPHS, ...DOOR_GLYPHS, ...PORTAL_GLYPHS].map((g) => [g, true])
);

/** True for glyphs that stay in `cells` (everything except the start arrows). */
export function isTileGlyph(ch: string): ch is Tile {
	return TILE_GLYPHS[ch] === true;
}

/** Lower-case key a door letter needs: 'A' -> 'a'. */
export function doorKey(door: string): string {
	return door.toLowerCase();
}

export const START_GLYPHS: Record<Facing, string> = { UP: '^', RIGHT: '>', DOWN: 'v', LEFT: '<' };
const FACING_BY_GLYPH: Record<string, Facing> = { '^': 'UP', '>': 'RIGHT', v: 'DOWN', '<': 'LEFT' };

export const MIN_SIDE = 3;
export const MAX_SIDE = 32;

export interface StartCell {
	x: number;
	y: number;
	facing: Facing;
}

export interface ParsedLayout {
	width: number;
	height: number;
	cells: Tile[][];
	/** First start glyph found, or null. */
	start: StartCell | null;
	startCount: number;
	treatCount: number;
	/** Key and door letters present, with repeats (so a doubled key shows up). */
	keyLetters: string[];
	doorLetters: string[];
	/** Portal digits present, with repeats: each pair must hold one digit twice. */
	portalDigits: string[];
}

/** Parses map layout strings (row 0 = top, y grows downward). Unknown glyphs read as empty. */
export function parseLayout(layout: string[]): ParsedLayout {
	const height = layout.length;
	const width = layout.reduce((w, row) => Math.max(w, row.length), 0);
	let start: StartCell | null = null;
	let startCount = 0;
	let treatCount = 0;
	const keyLetters: string[] = [];
	const doorLetters: string[] = [];
	const portalDigits: string[] = [];
	const cells: Tile[][] = layout.map((row, y) =>
		Array.from({ length: width }, (_, x) => {
			const ch = row[x] ?? '.';
			if (ch === '#') return '#';
			if (ch === '*') {
				treatCount++;
				return '*';
			}
			const facing = FACING_BY_GLYPH[ch];
			if (facing) {
				startCount++;
				start ??= { x, y, facing };
				return '.';
			}
			if (KEY_GLYPHS.includes(ch as (typeof KEY_GLYPHS)[number])) {
				keyLetters.push(ch);
				return ch as Tile;
			}
			if (DOOR_GLYPHS.includes(ch as (typeof DOOR_GLYPHS)[number])) {
				doorLetters.push(ch);
				return ch as Tile;
			}
			if (PORTAL_GLYPHS.includes(ch as (typeof PORTAL_GLYPHS)[number])) {
				portalDigits.push(ch);
				return ch as Tile;
			}
			return isTileGlyph(ch) ? ch : '.';
		})
	);
	return { width, height, cells, start, startCount, treatCount, keyLetters, doorLetters, portalDigits };
}

/** Serializes editor state back to layout strings. A start on a non-empty cell overrides it. */
export function formatLayout(cells: Tile[][], start: StartCell | null): string[] {
	return cells.map((row, y) =>
		row
			.map((tile, x) => (start && start.x === x && start.y === y ? START_GLYPHS[start.facing] : tile))
			.join('')
	);
}

/** Returns cells resized to width x height, keeping existing content and padding with empty. */
export function resizeCells(cells: Tile[][], width: number, height: number): Tile[][] {
	return Array.from({ length: height }, (_, y) =>
		Array.from({ length: width }, (_, x) => cells[y]?.[x] ?? '.')
	);
}

/** Creates an all-empty width x height grid. */
export function blankCells(width: number, height: number): Tile[][] {
	return resizeCells([], width, height);
}

/** Cheap client-side layout checks mirroring the server's rules, to give instant feedback. */
export function localLayoutIssues(p: ParsedLayout): string[] {
	const issues: string[] = [];
	if (p.width < MIN_SIDE || p.width > MAX_SIDE || p.height < MIN_SIDE || p.height > MAX_SIDE) {
		issues.push(`Grid must be ${MIN_SIDE}–${MAX_SIDE} cells per side.`);
	}
	if (p.startCount !== 1) issues.push('Place exactly one start cell.');
	if (p.treatCount < 1) issues.push('Place at least one treat.');
	// Mirrors the server's tile rules: keys and doors come in pairs, and each
	// portal digit must appear exactly twice (a portal needs a twin).
	const keys = new Set(p.keyLetters);
	const doors = new Set(p.doorLetters);
	for (const door of doors) {
		const key = doorKey(door);
		if (!keys.has(key)) issues.push(`Door '${door}' has no key '${key}' on this map.`);
	}
	for (const key of keys) {
		const door = key.toUpperCase();
		if (!doors.has(door)) issues.push(`Key '${key}' has no door '${door}' on this map.`);
	}
	for (const digit of new Set(p.portalDigits)) {
		const count = p.portalDigits.filter((d) => d === digit).length;
		if (count !== 2) issues.push(`Portal '${digit}' must appear exactly twice (found ${count}).`);
	}
	return issues;
}

/** True if two positions are equal. */
export function samePos(a: Position, b: Position): boolean {
	return a.x === b.x && a.y === b.y;
}

/** Max length of a map id, matching the server (^[a-z0-9_-]{1,64}$). */
export const MAX_MAP_ID = 64;

/**
 * Derives the id for a copy of `id`: `_copy` appended to the source id, with the
 * source trimmed to fit the server's limit and any trailing separator dropped so
 * the result reads cleanly. Always satisfies ^[a-z0-9_-]{1,64}$.
 */
export function copyId(id: string): string {
	const suffix = '_copy';
	const trimTail = (s: string) => s.replace(/[-_]+$/, '');
	const base = trimTail(id) || 'map';
	const head = trimTail(base.slice(0, Math.max(1, MAX_MAP_ID - suffix.length))) || 'map';
	return `${base.length + suffix.length <= MAX_MAP_ID ? base : head}${suffix}`;
}
