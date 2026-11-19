import appHtml from '../../app.html?raw';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
	DEFAULT_THEME,
	THEME_COLORS,
	THEME_KEY,
	applyTheme,
	parseChoice,
	readStoredChoice,
	resolveTheme,
	storeChoice,
	systemPrefersDark
} from './theme';

function stubMatchMedia(dark: boolean) {
	vi.stubGlobal(
		'matchMedia',
		vi.fn((q: string) => ({ matches: dark && q === '(prefers-color-scheme: dark)', addEventListener() {}, removeEventListener() {} }))
	);
}

beforeEach(() => {
	localStorage.clear();
	vi.unstubAllGlobals();
	vi.restoreAllMocks();
	document.documentElement.className = '';
	document.documentElement.removeAttribute('style');
	document.head.innerHTML = '<meta name="theme-color" content="#ffffff" />';
});

describe('parseChoice', () => {
	it.each(['system', 'light', 'dark'])('accepts %s', (v) => expect(parseChoice(v)).toBe(v));
	it.each([null, undefined, '', 'Dark', 'auto', 3])('falls back to the default for %s', (v) => expect(parseChoice(v)).toBe(DEFAULT_THEME));
});

describe('resolveTheme', () => {
	it.each([
		['system', false, 'light'],
		['system', true, 'dark'],
		['light', true, 'light'],
		['dark', false, 'dark']
	] as const)('%s with system dark=%s is %s', (choice, systemDark, want) => {
		expect(resolveTheme(choice, systemDark)).toBe(want);
	});
});

describe('systemPrefersDark', () => {
	it('reads the media query', () => {
		expect(systemPrefersDark({ matchMedia: () => ({ matches: true }) as MediaQueryList })).toBe(true);
		expect(systemPrefersDark({ matchMedia: () => ({ matches: false }) as MediaQueryList })).toBe(false);
	});

	it('is false when matchMedia is missing or throws', () => {
		expect(systemPrefersDark(undefined)).toBe(false);
		const throwing = {
			matchMedia: () => {
				throw new Error('nope');
			}
		} as unknown as Window;
		expect(systemPrefersDark(throwing)).toBe(false);
	});
});

describe('stored choice', () => {
	it('defaults to system', () => expect(readStoredChoice()).toBe('system'));

	it('round-trips light and dark under the project key prefix', () => {
		storeChoice('dark');
		expect(localStorage.getItem(THEME_KEY)).toBe('dark');
		expect(THEME_KEY.startsWith('drbrain-motor-programming.')).toBe(true);
		expect(readStoredChoice()).toBe('dark');
		storeChoice('light');
		expect(readStoredChoice()).toBe('light');
	});

	it('clears the key for system', () => {
		storeChoice('dark');
		storeChoice('system');
		expect(localStorage.getItem(THEME_KEY)).toBeNull();
	});

	it('ignores a corrupt value', () => {
		localStorage.setItem(THEME_KEY, 'purple');
		expect(readStoredChoice()).toBe('system');
	});

	it('survives storage that throws', () => {
		const blocked = () => {
			throw new Error('blocked');
		};
		vi.spyOn(Storage.prototype, 'getItem').mockImplementation(blocked);
		vi.spyOn(Storage.prototype, 'setItem').mockImplementation(blocked);
		vi.spyOn(Storage.prototype, 'removeItem').mockImplementation(blocked);
		expect(readStoredChoice()).toBe('system');
		expect(() => storeChoice('dark')).not.toThrow();
		expect(() => storeChoice('system')).not.toThrow();
	});
});

describe('applyTheme', () => {
	it('sets and clears the dark class, color-scheme and theme-color', () => {
		applyTheme('dark');
		const root = document.documentElement;
		expect(root.classList.contains('dark')).toBe(true);
		expect(root.style.colorScheme).toBe('dark');
		expect(document.querySelector('meta[name="theme-color"]')?.getAttribute('content')).toBe(THEME_COLORS.dark);
		applyTheme('light');
		expect(root.classList.contains('dark')).toBe(false);
		expect(root.style.colorScheme).toBe('light');
		expect(document.querySelector('meta[name="theme-color"]')?.getAttribute('content')).toBe(THEME_COLORS.light);
	});
});

// The inline script in app.html must agree with the functions above for every combination.
describe('app.html first-paint script', () => {
	const script = /<script>([\s\S]*?)<\/script>/.exec(appHtml)?.[1] ?? '';

	function runScript() {
		new Function(script)();
		return { dark: document.documentElement.classList.contains('dark'), scheme: document.documentElement.style.colorScheme };
	}

	it('is present and uses the same storage key', () => {
		expect(script).not.toBe('');
		expect(script).toContain(THEME_KEY);
	});

	it.each(['system', 'light', 'dark'] as const)('matches resolveTheme for choice %s', (choice) => {
		for (const systemDark of [false, true]) {
			document.documentElement.className = '';
			stubMatchMedia(systemDark);
			if (choice !== 'system') localStorage.setItem(THEME_KEY, choice);
			else localStorage.removeItem(THEME_KEY);
			const want = resolveTheme(choice, systemDark);
			expect(runScript()).toEqual({ dark: want === 'dark', scheme: want });
		}
	});

	it('follows the OS when storage throws', () => {
		stubMatchMedia(true);
		vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
			throw new Error('blocked');
		});
		expect(runScript().dark).toBe(true);
	});

	it('treats a corrupt stored value like system', () => {
		stubMatchMedia(true);
		localStorage.setItem(THEME_KEY, 'purple');
		expect(runScript().dark).toBe(true);
	});

	it('tints the theme-color meta when dark', () => {
		stubMatchMedia(true);
		runScript();
		expect(document.querySelector('meta[name="theme-color"]')?.getAttribute('content')).toBe(THEME_COLORS.dark);
	});
});
