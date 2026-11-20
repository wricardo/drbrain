import { describe, expect, it } from 'vitest';
import { GLYPH_LEGEND, buildGenericPrompt, buildSessionPrompt, layoutBlock, layoutHasTiles, tileRules } from './prompt';
import type { GameMap } from './types';

function map(over: Partial<GameMap> = {}): GameMap {
	return {
		id: 'tiny',
		name: 'Tiny',
		description: 'A tiny map.',
		difficulty: 'easy',
		builtin: false,
		width: 5,
		height: 3,
		layout: ['>...*', '.###.', '.....'],
		start: { x: 0, y: 0 },
		startFacing: 'RIGHT',
		rocks: [],
		treats: [{ x: 4, y: 0 }],
		conveyors: [],
		mirrors: [],
		keys: [],
		doors: [],
		portals: [],
		mainTapeLength: 3,
		subTapeLengths: [2, 4],
		maxSteps: 50,
		maxCallDepth: 6,
		allowRecursion: false,
		...over
	};
}

describe('layoutBlock', () => {
	it('adds an x ruler and y prefixes aligned with the cells', () => {
		expect(layoutBlock(['>.*', '...'])).toBe(['    012', 'y0  >.*', 'y1  ...'].join('\n'));
	});
	it('keeps columns aligned when there are 10+ rows', () => {
		const rows = Array.from({ length: 11 }, () => '...');
		const lines = layoutBlock(rows).split('\n');
		expect(lines[0].indexOf('0')).toBe(lines[1].indexOf('.'));
		expect(lines[11].indexOf('.')).toBe(lines[1].indexOf('.'));
	});
});

describe('buildSessionPrompt', () => {
	const p = buildSessionPrompt('http://h:1', { id: 'abc123', map: map() });

	it('carries the session id, endpoints and map facts the agent needs', () => {
		expect(p).toContain('Session ID: abc123');
		expect(p).toContain('http://h:1/graphql');
		expect(p).toContain('http://h:1/motor/llms.txt');
		expect(p).toContain('http://h:1/motor/watch/abc123');
		expect(p).toContain('Robot starts at (0,0) facing right (x+1)');
		expect(p).toContain('Treats (1): (4,0)');
		expect(p).toContain('main = 3 slots; sub1 (subs[0]) = 2 slots; sub2 (subs[1]) = 4 slots');
	});

	it('builds program skeletons with exactly the map tape lengths', () => {
		expect(p).toContain('main: [EMPTY, EMPTY, EMPTY]');
		expect(p).toContain('subs: [[EMPTY, EMPTY], [EMPTY, EMPTY, EMPTY, EMPTY]]');
		expect(p).toContain('setProgram(sessionID: "abc123"');
	});

	it('only offers calls to subs the map has', () => {
		expect(p).toContain('CALL_SUB_1, CALL_SUB_2.');
		expect(p).not.toContain('CALL_SUB_3');
		const none = buildSessionPrompt('http://h', { id: 'x', map: map({ subTapeLengths: [] }) });
		expect(none).not.toContain('CALL_SUB_1');
		expect(none).toContain('subs: []');
	});

	it('states the recursion rule that matches the map', () => {
		expect(p).toContain('Recursion is NOT allowed');
		const rec = buildSessionPrompt('http://h', { id: 'x', map: map({ allowRecursion: true }) });
		expect(rec).toContain('Recursion is ALLOWED');
		expect(rec).not.toContain('Recursion is NOT allowed');
	});

	it('does not depend on session state (stable text)', () => {
		expect(buildSessionPrompt('http://h:1', { id: 'abc123', map: map() })).toBe(p);
	});

	it('carries the owner token into the play calls, since changing the program is creator-only', () => {
		const shared = buildSessionPrompt('http://h:1', { id: 'abc123', map: map(), ownerToken: 'tok-secret' });
		expect(shared).toContain('tok-secret');
		// setProgram needs it; run/step carry it too, so the agent sees the program it runs.
		for (const call of ['setProgram(', 'run(', 'step(']) {
			const at = shared.indexOf(call);
			expect(at).toBeGreaterThan(-1);
			expect(shared.slice(at, shared.indexOf(')', at))).toContain('ownerToken: "tok-secret"');
		}
	});

	it('says so when no owner token is available (a stranger watching)', () => {
		expect(p).toContain('Owner token: (not available');
		expect(p).not.toContain('ownerToken: "');
	});
});

describe('leaderboard in prompts', () => {
	it('tells a session agent how winners are ranked and how to read its own rank', () => {
		const p = buildSessionPrompt('http://h:1', { id: 'abc123', map: map() });
		expect(p).toContain('ranked per map by slots used');
		expect(p).toContain('leaderboard(mapId: "tiny", sessionId: "abc123")');
	});
	it('mentions the board in the generic prompt', () => {
		expect(buildGenericPrompt('http://h:1')).toContain('leaderboard(mapId: "MAP_ID")');
	});
});

describe('buildGenericPrompt', () => {
	it('points the agent at llms.txt and the create-session flow', () => {
		const g = buildGenericPrompt('http://h:1');
		expect(g).toContain('http://h:1/motor/llms.txt');
		expect(g).toContain('createSession');
		expect(g).toContain('http://h:1/motor/watch/SESSION_ID');
	});
});

describe('tile rules in prompts', () => {
	it('always prints the glyph legend', () => {
		const p = buildSessionPrompt('http://h:1', { id: 'abc123', map: map() });
		expect(p).toContain(GLYPH_LEGEND);
		expect(buildGenericPrompt('http://h:1')).toContain(GLYPH_LEGEND);
	});

	it('stays quiet on a map with no special tiles', () => {
		expect(layoutHasTiles(['>...*', '.###.', '.....'])).toBe(false);
		expect(tileRules(map())).toBeNull();
		expect(buildSessionPrompt('http://h:1', { id: 'a', map: map() })).not.toContain('Tiles on this map');
	});

	it('detects tiles from the layout when the field lists are empty', () => {
		const tiled = map({ layout: ['>U..*', './/..', '..aA1'] });
		expect(layoutHasTiles(tiled.layout)).toBe(true);
		const p = buildSessionPrompt('http://h:1', { id: 'a', map: tiled });
		expect(p).toContain('## Tiles on this map');
		expect(p).toContain('pushed: true');
		expect(p).toContain('pushBlocked: true');
		expect(p).toContain('mirrored: true');
		expect(p).toContain('collectedKey');
		expect(p).toContain('teleported');
	});

	it('detects tiles from the map fields even without glyphs in the layout', () => {
		const fields = map({ conveyors: [{ pos: { x: 1, y: 0 }, dir: 'UP' }] });
		expect(tileRules(fields)).toContain('Conveyor');
	});

	it('names only the tile kinds the map uses', () => {
		const portalOnly = tileRules(map({ portals: [] , layout: ['>..1*', '....1', '.....'] }));
		expect(portalOnly).toContain('Portal');
		expect(portalOnly).not.toContain('Mirror');
		expect(portalOnly).not.toContain('Key (a b c)');
	});
});
