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
