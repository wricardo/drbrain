/**
 * Colour theme: the player's choice (System / Light / Dark), how it resolves to a concrete
 * theme, and how it is stored and applied to the page.
 *
 * `src/app.html` carries a tiny inline script that repeats the read-resolve-apply steps before
 * first paint (so there is no flash of the wrong theme). `theme.test.ts` runs that script against
 * these functions to keep the two in step; change both together.
 */

export type ThemeChoice = 'system' | 'light' | 'dark';
export type ResolvedTheme = 'light' | 'dark';

export const THEME_KEY = 'drbrain-motor-programming.theme';
export const THEME_CHOICES: readonly ThemeChoice[] = ['system', 'light', 'dark'];
export const DEFAULT_THEME: ThemeChoice = 'system';

/** Media query that matches when the OS asks for a dark colour scheme. */
export const DARK_QUERY = '(prefers-color-scheme: dark)';

/** Page background per theme, used for the browser's `theme-color` (address bar tint on phones). */
export const THEME_COLORS: Record<ResolvedTheme, string> = { light: '#ffffff', dark: '#171936' };

/** Narrows a stored value to a valid choice; anything unknown falls back to the default. */
export function parseChoice(value: unknown): ThemeChoice {
	return THEME_CHOICES.includes(value as ThemeChoice) ? (value as ThemeChoice) : DEFAULT_THEME;
}

/** The concrete theme for a choice, given whether the OS currently prefers dark. */
export function resolveTheme(choice: ThemeChoice, systemDark: boolean): ResolvedTheme {
	if (choice === 'system') return systemDark ? 'dark' : 'light';
	return choice;
}

/** Whether the OS prefers a dark colour scheme right now (false when it can't be asked). */
export function systemPrefersDark(win: Pick<Window, 'matchMedia'> | undefined = globalThis.window): boolean {
	try {
		return !!win?.matchMedia?.(DARK_QUERY).matches;
	} catch {
		return false;
	}
}

/** The saved choice, or the default when nothing valid is stored or storage is unavailable. */
export function readStoredChoice(): ThemeChoice {
	try {
		return parseChoice(localStorage.getItem(THEME_KEY));
	} catch {
		return DEFAULT_THEME;
	}
}

/** Saves the choice. "system" is the default, so it clears the key instead of storing it. */
export function storeChoice(choice: ThemeChoice): void {
	try {
		if (choice === DEFAULT_THEME) localStorage.removeItem(THEME_KEY);
		else localStorage.setItem(THEME_KEY, choice);
	} catch {
		// storage unavailable: the choice lasts for this page view only
	}
}

/** Applies a resolved theme to the document: the `dark` class, `color-scheme` and `theme-color`. */
export function applyTheme(theme: ResolvedTheme, doc: Document = document): void {
	const root = doc.documentElement;
	root.classList.toggle('dark', theme === 'dark');
	root.style.colorScheme = theme;
	doc.querySelector('meta[name="theme-color"]')?.setAttribute('content', THEME_COLORS[theme]);
}
