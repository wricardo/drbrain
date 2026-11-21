import type { GameMap } from './types';

/** The minimal slice of a session the prompt needs. */
import { TITLE } from './brand';

export interface PromptSession {
	id: string;
	map: GameMap;
	/**
	 * The session's owner token. Changing the program is creator-only, so an agent
	 * needs it for setProgram (playback does not need it, but a spectator can never
	 * read the program); a spectator view does not have it and omits it.
	 */
	ownerToken?: string | null;
}

const instructionList = (subCount: number): string =>
	['EMPTY', 'TURN_LEFT', 'TURN_RIGHT', 'MOVE_FORWARD', ...Array.from({ length: subCount }, (_, i) => `CALL_SUB_${i + 1}`)].join(', ');

const emptyTape = (n: number): string => `[${Array(n).fill('EMPTY').join(', ')}]`;

/** Renders the layout with an x ruler and a y prefix so an agent can read coordinates off it directly. */
export function layoutBlock(layout: string[]): string {
	const width = layout.reduce((w, r) => Math.max(w, r.length), 0);
	const pad = String(Math.max(0, layout.length - 1)).length;
	const ruler = ' '.repeat(pad + 3) + Array.from({ length: width }, (_, x) => String(x % 10)).join('');
	const rows = layout.map((row, y) => `y${String(y).padStart(pad, ' ')}  ${row}`);
	return [ruler, ...rows].join('\n');
}

const FACING_TEXT: Record<string, string> = { UP: 'up (y-1)', RIGHT: 'right (x+1)', DOWN: 'down (y+1)', LEFT: 'left (x-1)' };

/** One-line meaning of every layout glyph, shared by both prompts. */
export const GLYPH_LEGEND =
	"'.' empty, '#' rock (blocks), '*' treat, '^ > v <' robot start and facing, 'U D L R' conveyor (carries one extra cell), '/' and '\\' mirror (turns 90 degrees on arrival), 'a b c' key, 'A B C' door, '1'..'9' portal pair.";

/** True when a layout uses any glyph beyond the original floor/rock/treat/start set. */
export function layoutHasTiles(layout: string[]): boolean {
	return layout.some((row) => /[UDLR/\\abcABC1-9]/.test(row));
}

/**
 * The tile rules stated for an agent, or null when the map has no special tiles.
 * Carries, mirrors, keys, doors and portals all change the robot's position or
 * facing inside the same step, so an agent that does not know them will read a
 * correct run as a broken one.
 */
export function tileRules(m: Pick<GameMap, 'layout' | 'conveyors' | 'mirrors' | 'keys' | 'doors' | 'portals'>): string | null {
	const hasTiles =
		(m.conveyors?.length ?? 0) > 0 ||
		(m.mirrors?.length ?? 0) > 0 ||
		(m.keys?.length ?? 0) > 0 ||
		(m.doors?.length ?? 0) > 0 ||
		(m.portals?.length ?? 0) > 0 ||
		layoutHasTiles(m.layout);
	if (!hasTiles) return null;
	const lines = ['This map uses special tiles. They act inside the same step as your move, so treat them as part of the map, not a bug:'];
	if ((m.conveyors?.length ?? 0) > 0 || /[UDLR]/.test(m.layout.join('')))
		lines.push(
			'- Conveyor (U D L R): after a successful MOVE onto it the robot is carried ONE extra cell in that direction, in the same step ("pushed: true"). If that cell is a rock, the board edge or a closed door the robot stays on the conveyor ("pushBlocked: true"). The cell the conveyor lands on does not fire its own effect.'
		);
	if ((m.mirrors?.length ?? 0) > 0 || /[/\\]/.test(m.layout.join('')))
		lines.push(
			"- Mirror ('/' or '\\'): arriving on it reflects the facing 90 degrees (\"/\": right->up, up->right, down->left, left->down; \"\\\": right->down, down->right, up->left, left->up). The step reports \"mirrored: true\" and the new facing in facingAfter."
		);
	if ((m.keys?.length ?? 0) > 0 || /[abc]/.test(m.layout.join('')))
		lines.push(
			'- Key (a b c): collected by arriving on it (no extra step); it opens every matching door for the rest of the run. The step reports "collectedKey". Collecting a key is not needed to win.'
		);
	if ((m.doors?.length ?? 0) > 0 || /[ABC]/.test(m.layout.join('')))
		lines.push(
			'- Door (A B C): impassable like a rock until its key (same letter, lower case) has been collected. A move into a closed door is a no-op with "blocked: true"; it can also make a conveyor carry fail ("pushBlocked: true").'
		);
	if ((m.portals?.length ?? 0) > 0 || /[1-9]/.test(m.layout.join('')))
		lines.push(
			'- Portal (1..9): each digit appears on exactly two cells. Arriving on one teleports the robot to its twin in the same step and "teleported" gives the destination; the destination cell does not fire its own effect. Where the robot ends up after any tile effect is the "to" position.'
		);
	lines.push('- At most one tile effect applies per step, and treats or keys are collected on the cell the robot finishes on.');
	return lines.join('\n');
}

/**
 * Prompt that makes an AI agent play (and win) one specific, existing session through the GraphQL API.
 * Built only from the map, so it stays stable while the session changes. It never contains a solution.
 */
export function buildSessionPrompt(origin: string, s: PromptSession): string {
	const m = s.map;
	const subs = m.subTapeLengths;
	const tapes = [`main = ${m.mainTapeLength} slots`, ...subs.map((n, i) => `sub${i + 1} (subs[${i}]) = ${n} slots`)].join('; ');
	const skeleton = `main: ${emptyTape(m.mainTapeLength)}\n    subs: [${subs.map(emptyTape).join(', ')}]`;
	const treats = m.treats.map((t) => `(${t.x},${t.y})`).join(' ');
	const ownerTokenLine = s.ownerToken
		? `Owner token (this session's programming credential, keep it private — setProgram below needs it): ${s.ownerToken}`
		: 'Owner token: (not available — you cannot change this session\'s program from here; you can still run, step, pause and reset it below)';
	// Only setProgram needs the token: playback is open to anyone, but never returns the program.
	const tokenArg = s.ownerToken ? `, ownerToken: "${s.ownerToken}"` : '';
	const recursion = m.allowRecursion
		? `Recursion is ALLOWED on this map: a sub may call itself (or any sub already running). The call stack is bounded by maxCallDepth = ${m.maxCallDepth} and by the step limit, and there is no tail-call optimisation (the stack stays deep until a sub finishes).`
		: `Recursion is NOT allowed on this map: a CALL_SUB_k aimed at the sub that is running, or at any sub already waiting on the call stack, does nothing (the step is still spent). A sub may call a different sub that is not on the stack, so you can nest subs; maxCallDepth = ${m.maxCallDepth}.`;

	const tiles = tileRules(m);

	return `Use this GraphQL API to play an existing ${TITLE} session and WIN it.

${TITLE} is a robot programming puzzle. You do not steer the robot: you write it a program (a main tape plus ${subs.length} subroutine ${subs.length === 1 ? 'tape' : 'tapes'} of instructions), the server runs it one instruction per step, and you win the moment every treat has been collected.

Session ID: ${s.id}
${ownerTokenLine}
GraphQL endpoint: ${origin}/graphql
Playground: ${origin}/graphql/playground
Full rules and every field: ${origin}/motor/llms.txt
Spectators can watch live at: ${origin}/motor/watch/${s.id}

GraphQL introspection is usually enabled; use the Playground or __schema/__type if you need to discover fields.

## This map: ${m.name} (${m.width}x${m.height}, ${m.difficulty})
${m.description}

Layout (x = column, y = row; row 0 is the TOP and y grows DOWNWARD):
${layoutBlock(m.layout)}

${GLYPH_LEGEND}
Robot starts at (${m.start.x},${m.start.y}) facing ${FACING_TEXT[m.startFacing] ?? m.startFacing}.
Treats (${m.treats.length}): ${treats}

Tapes (setProgram needs EXACTLY these lengths; pad unused slots with EMPTY): ${tapes}.
Limits: at most ${m.maxSteps} steps; ${recursion}

${tiles ? `## Tiles on this map\n${tiles}\n` : ''}
## Rules that matter
- Instructions: ${instructionList(subs.length)}.
- Each executed instruction is one step (a blocked move and a CALL both count). EMPTY slots are skipped for free.
- MOVE_FORWARD into a rock or off the map is a harmless no-op (still a step, "blocked: true"). Treats are collected on arrival.
- When recursion is off, a CALL to a sub already on the call stack is ignored but still costs a step ("callBlocked: true").
- When a sub tape ends, control returns to the caller; when the main tape ends with treats left, you lose (PROGRAM_ENDED). Other losses: STEP_LIMIT, CALL_DEPTH.
- Win beats the step limit on the same step. setProgram and reset put the robot back at the start. setProgram is rejected while the session is playing (SESSION_PLAYING); finished sessions refuse run/step until reset (SESSION_TERMINAL).

## How to win
1. Plan on paper first: write the route as straight runs and turns using the coordinates above. Tapes are tiny, so look for repeated patterns and put them in subroutines (a sub can be a "row", a "U-turn", a "spiral side"...). Remember a blocked move costs a step but does no harm, so an over-long run of MOVE_FORWARD is safe when a wall will stop it.
2. Store the program in the session:
mutation {
  motorProgramming {
    setProgram(sessionID: "${s.id}"${tokenArg}, program: {
      ${skeleton}
    }) { id vm { status steps } }
  }
}
   (Replace the EMPTY slots with real instructions.)
3. Run it on the server (50-5000 ms per step) or step manually, then read the result:
mutation { motorProgramming { run(sessionID: "${s.id}"${tokenArg}, speedMs: 100) { playing vm { status steps } } } }
mutation { motorProgramming { step(sessionID: "${s.id}"${tokenArg}) { vm { status steps pos { x y } } lastEvent { instruction blocked callBlocked pushed pushBlocked mirrored collectedKey teleported { x y } } } } }
query { motorProgramming { session(id: "${s.id}") { playing attempts bestSteps vm { status lossReason steps pos { x y } facing treatsRemaining { x y } callStack { tape pc } } } } }
4. If the status is LOST, read lossReason and treatsRemaining, fix the program, and repeat from step 2 (call reset first if you want to run again without changing the program). Do not stop until vm.status is WON, then report the winning program and its step count.
5. Winners are ranked per map by slots used (non-EMPTY slots across all tapes; fewer is better), then by steps. Once you have won, you may try to win again with fewer slots or steps (setProgram, then run) and read your rank:
query { motorProgramming { leaderboard(mapId: "${s.map.id}", sessionId: "${s.id}") { total yours { rank slots steps } entries { rank displayName slots steps } } } }

Other mutations: pause, reset, deleteSession. Do not try to guess other sessions' ids and do not delete this one.`;
}

/**
 * Generic prompt (no session yet): the agent reads llms.txt, creates its own session on a map it picks and wins it.
 */
export function buildGenericPrompt(origin: string): string {
	return `Play ${TITLE}, a robot programming puzzle, through its GraphQL API and win it.

You write the robot a program (a main tape plus up to 10 subroutine tapes); the server runs it one instruction per step; you win when every treat has been collected. Tapes are tiny, so the trick is spotting repeated patterns and reusing them with subroutines.

1. Read ${origin}/motor/llms.txt first: it has the complete rules, the coordinate convention (row 0 is the top, y grows downward) and working examples for every step.
   Layout glyphs: ${GLYPH_LEGEND} Conveyors carry the robot one extra cell, mirrors turn it, portals teleport it to their twin, keys open matching doors — all inside the same step, so they are never a bug.
2. GraphQL endpoint: ${origin}/graphql (Playground: ${origin}/graphql/playground).
3. List the maps: query { motorProgramming { maps { id name difficulty layout mainTapeLength subTapeLengths maxSteps maxCallDepth allowRecursion } } } and pick one (start with an easy one).
4. Create your own session: mutation { motorProgramming { createSession(mapID: "MAP_ID") { id ownerToken } } }. A session that belongs to no account is shown on the leaderboard as "Guest".
5. KEEP the ownerToken that createSession returned. Changing a session's program is creator-only: setProgram needs it (ownerToken: "..."), otherwise it answers FORBIDDEN. run, step, pause and reset work without it but never return the program, so the token is what lets you read back the program you are writing. Plan the route from the layout, then setProgram, run, and read the session until vm.status is WON. If it is LOST, read lossReason, fix the program and try again.
6. Tell me the session id so I can watch it live at ${origin}/motor/watch/SESSION_ID, and report the winning program and its step count. Winners are ranked per map by fewest slots used (non-EMPTY slots across all tapes), then fewest steps: query { motorProgramming { leaderboard(mapId: "MAP_ID") { entries { rank displayName slots steps } } } }.`;
}
