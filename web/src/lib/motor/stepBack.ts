/**
 * Step back = start over and replay one step fewer. The engine is deterministic, so replaying the same
 * program for `target` steps lands on exactly the state the robot was in one step earlier. The server
 * has no "undo step" call, so this uses the ordinary reset and step calls.
 */
export interface ReplayState {
	vm: { status: string };
}

const TERMINAL = new Set(['WON', 'LOST']);

/**
 * Resets, then steps `target` times (stopping early if the run ends). Returns the final snapshot.
 * Never steps past a finished run: replaying fewer steps than the original run never reaches the end.
 */
export async function replayTo<S extends ReplayState>(target: number, reset: () => Promise<S>, step: () => Promise<S>): Promise<S> {
	let s = await reset();
	for (let i = 0; i < target && !TERMINAL.has(s.vm.status); i++) s = await step();
	return s;
}

/** Steps to replay to go back one step from `steps` taken so far, or null when already at the start. */
export function stepBackTarget(steps: number): number | null {
	return steps > 0 ? steps - 1 : null;
}
