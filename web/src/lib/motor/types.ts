// Types for the motor UI. The GraphQL schema generates the shared shapes in
// ../graphql-types.ts; this file keeps the game-local names used by screens.
export type {
	Motor_ClaimStatus as ClaimStatus,
	Motor_Conveyor as Conveyor,
	Motor_DoorTile as DoorTile,
	Motor_Facing as Facing,
	Motor_Frame as Frame,
	Motor_Instruction as Instruction,
	Motor_KeyTile as KeyTile,
	Motor_Leaderboard as Leaderboard,
	Motor_LeaderboardEntry as LeaderboardEntry,
	Motor_LeaderboardOrder as LeaderboardOrder,
	Motor_LossReason as LossReason,
	Motor_Map as GameMap,
	Motor_MapInput as MapInput,
	Motor_MapValidationResult as MapValidationResult,
	Motor_Mirror as Mirror,
	Motor_PlayerRun as PlayerRun,
	Motor_Portal as Portal,
	Motor_Position as Position,
	Motor_Program as Program,
	Motor_Session as Session,
	Motor_SessionClaimInput as SessionClaim,
	Motor_SessionClaimResult as SessionClaimResult,
	Motor_SessionUpdate as SessionUpdate,
	Motor_Status as Status,
	Motor_StepEvent as StepEvent,
	Motor_VMState as VMState
} from '../graphql-types';

import type { Motor_Position as Position, Motor_Status as Status } from '../graphql-types';

/** Lightweight session row used by the recent-sessions list. */
export interface SessionSummary {
	id: string;
	mapId: string;
	playing: boolean;
	createdAt: string;
	lastActionAt: string;
	map: { name: string };
	vm: { status: Status; steps: number; treatsRemaining: Position[] };
}

export type ConnectionState = 'connecting' | 'live' | 'reconnecting' | 'offline';
