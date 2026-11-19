import { describe, expect, it } from 'vitest';
import { isUnlocked, lockColor, lockPairText, lockPairs, lockTone, lockInk } from './tiles';

describe('lock tiles', () => {
	it('gives each key letter its own tone, case-insensitively', () => {
		expect(lockTone('a')).toBe('a');
		expect(lockTone('B')).toBe('b');
		expect(lockTone('c')).toBe('c');
		expect(lockColor('b')).toBe('var(--board-lock-b)');
		expect(lockInk('C')).toBe('var(--board-lock-c-ink)');
	});

	it('falls back to the first tone for a letter no shipped map uses', () => {
		expect(lockTone('z')).toBe('a');
		expect(lockColor('')).toBe('var(--board-lock-a)');
	});

	it('reads open doors from the run state, ignoring case', () => {
		expect(isUnlocked([], 'a')).toBe(false);
		expect(isUnlocked(['a'], 'A')).toBe(true);
		expect(isUnlocked(['b', 'c'], 'a')).toBe(false);
	});

	it('pairs each key with the door letter it opens', () => {
		const pairs = lockPairs([{ letter: 'a' }, { letter: 'b' }], [{ letter: 'A' }, { letter: 'B' }], ['a']);
		expect(pairs).toEqual([
			{ key: 'a', door: 'A', open: true },
			{ key: 'b', door: 'B', open: false }
		]);
		expect(lockPairText(pairs)).toBe('a opens A, b opens B');
	});

	it('keeps a door that has no key instead of hiding it', () => {
		// NewMap rejects this, so it can only be a hand-edited user map: say so rather than
		// drawing a door the player cannot open with no explanation.
		const pairs = lockPairs([{ letter: 'a' }], [{ letter: 'A' }, { letter: 'C' }], []);
		expect(pairs.map((p) => p.door)).toEqual(['A', 'C']);
		expect(lockPairText(pairs)).toBe('a opens A, C has no key');
	});
});
