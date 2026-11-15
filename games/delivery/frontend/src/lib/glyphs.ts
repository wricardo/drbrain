// Glyph handling for the ASCII city the server sends in `city.rows`.
//
// The server is the source of truth for what a tile is, so the UI never tries
// to re-derive terrain from entity positions: it reads the glyph and styles it.

export interface GlyphStyle {
	/** CSS class for the tile background. */
	cls: string;
	/** Short human label, used in tooltips and the legend. */
	label: string;
	/** True when a car may enter the tile (used for cursor affordance). */
	passable: boolean;
}

const STYLES: Record<string, GlyphStyle> = {
	'#': { cls: 'bg-[var(--color-water)]', label: 'Water — impassable', passable: false },
	B: { cls: 'bg-ink-600', label: 'Building — impassable', passable: false },
	'.': { cls: 'bg-[var(--color-road)]', label: 'Road', passable: true },
	',': { cls: 'bg-[var(--color-ground)]', label: 'Ground — costs 2 battery', passable: true },
	D: { cls: 'bg-[var(--color-depot)]', label: 'Depot — parks and recharges fast', passable: true },
	C: { cls: 'bg-[var(--color-charger)]', label: 'Charger', passable: true },
	K: { cls: 'bg-[var(--color-kitchen)]', label: 'Kitchen — pickup', passable: true },
	H: { cls: 'bg-[var(--color-customer)]', label: 'Customer — dropoff', passable: true },
	'?': { cls: 'bg-ink-950 border border-ink-700', label: 'Unexplored — impassable', passable: false }
};

/** Unknown glyphs are treated as impassable scenery rather than guessed at. */
export function glyphStyle(ch: string): GlyphStyle {
	return STYLES[ch] ?? { cls: 'bg-ink-700', label: 'Unknown', passable: false };
}

/** Cars are drawn as the digits 1-9, matching their index in the cars array. */
export function carIndex(ch: string): number | null {
	if (ch >= '1' && ch <= '9') return Number(ch) - 1;
	return null;
}

/**
 * Battery colour band. The thresholds match the engine's intent: under 25% you
 * may not reach a charger, so it reads as danger rather than decoration.
 */
export function batteryTone(pct: number): string {
	if (pct <= 10) return 'text-[var(--color-danger)]';
	if (pct <= 25) return 'text-[var(--color-warn)]';
	if (pct <= 60) return 'text-[var(--color-charger)]';
	return 'text-[var(--color-money)]';
}

/** Deadline tone for an order's remaining time. */
export function deadlineTone(ticksLeft: number): string {
	if (ticksLeft < 0) return 'text-[var(--color-danger)]';
	if (ticksLeft <= 8) return 'text-[var(--color-danger)]';
	if (ticksLeft <= 20) return 'text-[var(--color-warn)]';
	return 'text-fog-400';
}
