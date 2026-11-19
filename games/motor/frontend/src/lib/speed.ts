/** Playback speed presets: milliseconds per step (the server accepts 50-5000). */
export interface SpeedPreset {
	id: string;
	label: string;
	ms: number;
}

export const SPEED_PRESETS: readonly SpeedPreset[] = [
	{ id: 'slow', label: 'Slow', ms: 1000 },
	{ id: 'normal', label: 'Normal', ms: 500 },
	{ id: 'fast', label: 'Fast', ms: 200 },
	{ id: 'turbo', label: 'Turbo', ms: 50 }
];

/** The preset exactly matching `ms`, or null for a custom speed (e.g. one set through the API). */
export function presetFor(ms: number): SpeedPreset | null {
	return SPEED_PRESETS.find((p) => p.ms === ms) ?? null;
}

/** Clamps a session speed into the range the UI and server accept. */
export function clampSpeed(ms: number): number {
	return Math.max(50, Math.min(5000, ms));
}

const SPEED_KEY = 'drbrain-motor-programming.speedMs';

/** The speed the player last chose in this browser, or null when none is saved. */
export function savedSpeed(): number | null {
	try {
		const raw = localStorage.getItem(SPEED_KEY);
		const n = raw === null ? NaN : Number(raw);
		return Number.isFinite(n) ? clampSpeed(n) : null;
	} catch {
		return null;
	}
}

/** Remembers the chosen speed so the next map starts at it. */
export function saveSpeed(ms: number): void {
	try {
		localStorage.setItem(SPEED_KEY, String(clampSpeed(ms)));
	} catch {
		// storage unavailable: the speed just resets to Normal next time
	}
}

/**
 * The speed a session opens at: its own when it has been run before, otherwise the one the player last
 * chose (a brand-new session would otherwise always start at the server default).
 */
export function initialSpeed(session: { speedMs?: number; attempts: number; vm: { steps: number } }, saved: number | null): number {
	const fresh = session.attempts === 0 && session.vm.steps === 0;
	if (fresh && saved !== null) return saved;
	return clampSpeed(session.speedMs || 500);
}
