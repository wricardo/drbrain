// Hand-written types mirroring graph/schema.graphqls. Keep in sync with the schema.

export type Instruction =
	| 'EMPTY'
	| 'TURN_LEFT'
	| 'TURN_RIGHT'
	| 'MOVE_FORWARD'
	| 'CALL_SUB_1'
	| 'CALL_SUB_2'
	| 'CALL_SUB_3';

export type Facing = 'UP' | 'RIGHT' | 'DOWN' | 'LEFT';
export type Status = 'READY' | 'RUNNING' | 'WON' | 'LOST';
export type LossReason = 'STEP_LIMIT' | 'CALL_DEPTH' | 'PROGRAM_ENDED';

/** Coordinates: x = column, y = row; y grows downward. */
export interface Position {
	x: number;
	y: number;
}

/** One entry of the VM call stack. tape -1 is the main tape; pc is the next instruction index. */
export interface Frame {
	tape: number;
	pc: number;
}

export interface GameMap {
	id: string;
	name: string;
	description: string;
	difficulty: string;
	/** True for a map shipped with the game: only an admin may change or delete it. */
	builtin: boolean;
	width: number;
	height: number;
	layout: string[];
	start: Position;
	startFacing: Facing;
	rocks: Position[];
	treats: Position[];
	mainTapeLength: number;
	subTapeLengths: number[];
	maxSteps: number;
	maxCallDepth: number;
	allowRecursion: boolean;
}

export interface Program {
	main: Instruction[];
	subs: Instruction[][];
}

export interface VMState {
	pos: Position;
	facing: Facing;
	treatsRemaining: Position[];
	callStack: Frame[];
	steps: number;
	status: Status;
	lossReason: LossReason | null;
	visited: Position[];
}

export interface StepEvent {
	step: number;
	instruction: Instruction;
	tapeIndex: number;
	slotIndex: number;
	from: Position;
	to: Position;
	facingBefore: Facing;
	facingAfter: Facing;
	blocked: boolean;
	callBlocked: boolean;
	collected: Position | null;
	callStack: Frame[];
	status: Status;
	lossReason: LossReason | null;
}

export interface Session {
	id: string;
	displayName: string;
	mapId: string;
	map: GameMap;
	program: Program;
	vm: VMState;
	playing: boolean;
	speedMs: number;
	seq: number;
	createdAt: string;
	lastActionAt: string;
	attempts: number;
	bestSteps: number | null;
	lastEvent: StepEvent | null;
	/** Secret proving who created the session. Only the createSession response carries it; null everywhere else. */
	ownerToken?: string | null;
}

export interface SessionUpdate {
	seq: number;
	session: Session;
	event: StepEvent | null;
}

/** Lightweight session row used by the recent-sessions list. */
export interface SessionSummary {
	id: string;
	displayName: string;
	mapId: string;
	playing: boolean;
	createdAt: string;
	lastActionAt: string;
	map: { name: string };
	vm: { status: Status; steps: number; treatsRemaining: Position[] };
}

export interface MapValidationResult {
	valid: boolean;
	issues: { message: string }[];
	unreachableTreats: Position[];
}

export interface MapInput {
	id: string;
	name: string;
	description?: string;
	difficulty?: string;
	layout: string[];
	mainTapeLength?: number;
	subTapeLengths?: number[];
	maxSteps?: number;
	maxCallDepth?: number;
	allowRecursion?: boolean;
}

/** WebSocket connection indicator for spectator/play views. */
export type ConnectionState = 'connecting' | 'live' | 'reconnecting' | 'offline';

/** Leaderboard ordering: fewest slots first (default) or fewest steps first. */
export type LeaderboardOrder = 'SLOTS' | 'STEPS';

/** One ranked result. Slots and steps are recorded by the server when the run is won. */
export interface LeaderboardEntry {
	/** 1-based; entries tied on both slots and steps share a rank. */
	rank: number;
	sessionId: string;
	/** An account's entry shows its display name, or "Private player" for a private profile. */
	displayName: string;
	/** Handle of the signed-in player who owns the entry (profile at /u/<handle>); null for guests and private profiles. */
	handle: string | null;
	/** True when the entry belongs to the signed-in viewer's account. */
	mine: boolean;
	slots: number;
	steps: number;
	achievedAt: string;
}

export interface Leaderboard {
	mapId: string;
	order: LeaderboardOrder;
	/** Ranked sessions on this board, not just the rows returned. */
	total: number;
	entries: LeaderboardEntry[];
	/** The entry of the requested session, even beyond the returned rows; null when it has not won. */
	yours: LeaderboardEntry | null;
}

/** A player's best result on one map: their best winning session on the SLOTS board and its current rank. */
export interface PlayerRun {
	mapId: string;
	/** Ranked sessions on that map's SLOTS board. */
	total: number;
	entry: LeaderboardEntry;
}

/** The signed-in player. Only a handle and names are kept: no email, no avatar. */
export interface Account {
	handle: string;
	displayName: string;
	profilePublic: boolean;
	createdAt: string;
	/** When the handle may be changed again; null when it can be changed now. */
	handleChangeableAt: string | null;
	/** True for a username/password account (the only kind that can change a password). */
	hasPassword: boolean;
}

/** A way to sign in that this server has enabled. Sign in by navigating to /auth/<id>/login. */
export interface AuthProvider {
	id: string;
	name: string;
}

/** A public profile page: the account and its best run per map. */
export interface Profile {
	handle: string;
	displayName: string;
	createdAt: string;
	isMine: boolean;
	runs: PlayerRun[];
}

export type ClaimStatus = 'ATTACHED' | 'ALREADY_YOURS' | 'REJECTED';

export interface SessionClaim {
	sessionID: string;
	ownerToken: string;
}

export interface SessionClaimResult {
	sessionID: string;
	status: ClaimStatus;
}

export interface AccountInput {
	handle?: string;
	displayName?: string;
	profilePublic?: boolean;
}
