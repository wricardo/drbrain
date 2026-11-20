// Types for the mazes UI. The GraphQL schema generates the shared shapes in
// ../graphql-types.ts; this file keeps the game-local names used by screens.
export type {
	Mazes_AdminMap as AdminMap,
	Mazes_Direction as Direction,
	Mazes_GameState as GameState,
	Mazes_LevelView as LevelView,
	Mazes_LossReason as LossReason,
	Mazes_Map as GameMap,
	Mazes_MapInput as MapInput,
	Mazes_MapValidationResult as MapValidationResult,
	Mazes_MoveEvent as MoveEvent,
	Mazes_Portal as Portal,
	Mazes_Position as Position,
	Mazes_Session as Session,
	Mazes_SessionUpdate as SessionUpdate,
	Mazes_Status as Status,
	Mazes_Via as Via
} from '../graphql-types';

import type { Mazes_Status as Status } from '../graphql-types';

/** Lightweight session row used by the recent-sessions list. */
export interface SessionSummary {
	id: string;
	displayName: string;
	mapId: string;
	createdAt: string;
	lastActionAt: string;
	map: { name: string; parMoves: number | null; maxMoves: number };
	state: { status: Status; moves: number };
}

export type ConnectionState = 'connecting' | 'live' | 'reconnecting' | 'offline';
