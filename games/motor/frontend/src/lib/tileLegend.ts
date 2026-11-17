import type { GameMap } from './types';

/** A kind of board tile a map can use, in one line of player language. */
export interface TileKind {
	id: 'conveyor' | 'mirror' | 'key' | 'door' | 'portal';
	name: string;
	/** What the tile looks like on the board. */
	glyph: string;
	text: string;
}

const KINDS: Record<TileKind['id'], TileKind> = {
	conveyor: {
		id: 'conveyor',
		name: 'Conveyor',
		glyph: '↑ → ↓ ←',
		text: 'Carries you one extra cell the way it points, in the same step. A rock, the board edge or a closed door stops the carry.'
	},
	mirror: {
		id: 'mirror',
		name: 'Mirror',
		glyph: '╱ ╲',
		text: 'Turns you 90° when you arrive. A slash swaps right with up and left with down; a backslash swaps right with down and left with up.'
	},
	key: { id: 'key', name: 'Key', glyph: 'a b c', text: 'Walk over it to pick it up. It opens every door with the same letter.' },
	door: { id: 'door', name: 'Door', glyph: 'A B C', text: 'Solid like a rock until its key (the same letter) has been picked up.' },
	portal: { id: 'portal', name: 'Portal', glyph: '1 … 9', text: 'Arriving teleports you to the portal with the same digit, in the same step.' }
};

/** The tile kinds this map uses, in a stable order. Empty for a map with plain floor, rocks and treats only. */
export function tileKindsOnMap(map: Pick<GameMap, 'conveyors' | 'mirrors' | 'keys' | 'doors' | 'portals'>): TileKind[] {
	const out: TileKind[] = [];
	if (map.conveyors?.length) out.push(KINDS.conveyor);
	if (map.mirrors?.length) out.push(KINDS.mirror);
	if (map.keys?.length) out.push(KINDS.key);
	if (map.doors?.length) out.push(KINDS.door);
	if (map.portals?.length) out.push(KINDS.portal);
	return out;
}

/** Said once under the list: tiles never cost extra steps and never chain. */
export const TILE_NOTE = 'A tile effect happens inside the same step, so it costs nothing extra, and at most one fires per step.';
