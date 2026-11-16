/**
 * Lock tiles: a key letter (a, b, c) opens every door with the same letter
 * (engine: `Map.DoorLetters` / `VMState.DoorsOpen`). Grid.svelte and MiniGrid.svelte
 * paint both ends in one tone per letter, so a key and the door it opens match.
 */

export const LOCK_TONES = ['a', 'b', 'c'] as const;
export type LockTone = (typeof LOCK_TONES)[number];

/** Tone for a key or door letter. Unknown letters fall back to 'a' so a custom map still paints. */
export function lockTone(letter: string): LockTone {
	const lower = letter.toLowerCase();
	return (LOCK_TONES as readonly string[]).includes(lower) ? (lower as LockTone) : 'a';
}

/** Fill for the key glyph and the door leaf. */
export function lockColor(letter: string): string {
	return `var(--board-lock-${lockTone(letter)})`;
}

/** Text/knob colour that reads on top of `lockColor`. */
export function lockInk(letter: string): string {
	return `var(--board-lock-${lockTone(letter)}-ink)`;
}

/** True when this door's letter is in the run's open list. The engine lowercases door letters. */
export function isUnlocked(openDoors: Iterable<string>, letter: string): boolean {
	const lower = letter.toLowerCase();
	for (const open of openDoors) if (open.toLowerCase() === lower) return true;
	return false;
}

export type LockPair = { key: string; door: string; open: boolean };

/**
 * Pairs each key with the door letter it opens, in board order, for the board's
 * accessible description. Doors without a key are kept (`key: ''`): `NewMap`
 * rejects them, so they can only come from a hand-edited user map.
 */
export function lockPairs(
	keys: readonly { letter: string }[],
	doors: readonly { letter: string }[],
	openDoors: Iterable<string>
): LockPair[] {
	const pairs: LockPair[] = keys.map((k) => ({
		key: k.letter,
		door: k.letter.toUpperCase(),
		open: isUnlocked(openDoors, k.letter)
	}));
	for (const d of doors) {
		const door = d.letter.toUpperCase();
		if (pairs.some((p) => p.door === door)) continue;
		pairs.push({ key: '', door, open: isUnlocked(openDoors, d.letter) });
	}
	return pairs;
}

/** "a opens A, B has no key" — the pairing clause appended to the board label. */
export function lockPairText(pairs: readonly LockPair[]): string {
	return pairs
		.map((p) => (p.key ? `${p.key} opens ${p.door}` : `${p.door} has no key`))
		.join(', ');
}
