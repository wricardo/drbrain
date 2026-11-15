import { get, writable, type Readable } from 'svelte/store';
import {
	DARK_QUERY,
	applyTheme,
	readStoredChoice,
	resolveTheme,
	storeChoice,
	systemPrefersDark,
	type ResolvedTheme,
	type ThemeChoice
} from '$lib/theme';

const choice = writable<ThemeChoice>('system');
const resolved = writable<ResolvedTheme>('light');

/** The player's choice: System, Light or Dark. */
export const themeChoice: Readable<ThemeChoice> = { subscribe: choice.subscribe };
/** The theme actually shown. */
export const resolvedTheme: Readable<ResolvedTheme> = { subscribe: resolved.subscribe };

function update(next: ThemeChoice): void {
	const theme = resolveTheme(next, systemPrefersDark());
	choice.set(next);
	resolved.set(theme);
	applyTheme(theme);
}

/** Changes and remembers the choice, and applies it at once. */
export function setThemeChoice(next: ThemeChoice): void {
	storeChoice(next);
	update(next);
}

/**
 * Loads the saved choice and, while the choice is "system", follows the OS setting as it
 * changes. Call once from the root layout; returns a function that stops following.
 */
export function initTheme(): () => void {
	update(readStoredChoice());
	let query: MediaQueryList | undefined;
	try {
		query = window.matchMedia(DARK_QUERY);
	} catch {
		return () => {};
	}
	const onChange = () => {
		if (get(choice) === 'system') update('system');
	};
	query.addEventListener?.('change', onChange);
	return () => query?.removeEventListener?.('change', onChange);
}
