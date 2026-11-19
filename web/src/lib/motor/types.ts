// Hand-written types mirroring graph/schema.graphqls. Keep in sync with the schema.

export type Instruction =
	| 'EMPTY'
	| 'TURN_LEFT'
	| 'TURN_RIGHT'
	| 'MOVE_FORWARD'
	| 'CALL_SUB_1'
	| 'CALL_SUB_2'
	| 'CALL_SUB_3'
	| 'CALL_SUB_4'
	| 'CALL_SUB_5'
	| 'CALL_SUB_6'
	| 'CALL_SUB_7'
	| 'CALL_SUB_8'
	| 'CALL_SUB_9'
	| 'CALL_SUB_10';

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

/** A one-way tile: a move onto it is carried one extra cell in `dir`. */
export interface Conveyor {
	pos: Position;
	dir: Facing;
}

/** A mirror: `slash` true is '/', false is '\\'; both reflect the facing 90 degrees on arrival. */
export interface Mirror {
	pos: Position;
	/** True for '/', false for '\\'. */
	slash: boolean;
}

/** A collectable key ('a'..'c'); each one opens every matching door. */
export interface KeyTile {
	pos: Position;
	/** Lower-case key letter ('a'..'c'). */
	letter: string;
}

/** A door ('A'..'C'), impassable until its key is collected. */
export interface DoorTile {
	pos: Position;
	/** Upper-case door letter ('A'..'C'). */
	letter: string;
}

/** One end of a portal pair ('1'..'9'); arriving teleports to the twin. */
export interface Portal {
	pos: Position;
	/** Digit shared by the two ends of the pair. */
	pair: string;
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
	/** One-way tiles: a move onto one is carried one extra cell in Conveyor.dir. */
	conveyors: Conveyor[];
	/** Mirrors force a 90-degree turn on arrival: slash=true is '/', false is '\\'. */
	mirrors: Mirror[];
	/** Collectable keys ('a'..'c'); each opens every matching door. */
	keys: KeyTile[];
	/** Doors ('A'..'C'), impassable until their key is collected. */
	doors: DoorTile[];
	/** Portal pairs ('1'..'9'); arriving on one teleports to the other. */
	portals: Portal[];
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
	/** Lower-case letters of the doors this run has opened, e.g. ['a']. */
	openDoors: string[];
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
	/** The move landed on a conveyor and was carried one extra cell in the same step. */
	pushed: boolean;
	/** A conveyor carry was blocked (rock, edge or closed door), so the robot stayed on the conveyor. */
	pushBlocked: boolean;
	/** The arrival tile was a mirror and reflected the facing. */
	mirrored: boolean;
	/** The key picked up on this step, or null. */
	collectedKey: string | null;
	/** Portal destination when the arrival tile was a portal, or null. */
	teleported: Position | null;
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
	/** Null for anyone who may not play this session: a program is never a public answer key. */
	program: Program | null;
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
