import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import ThemeToggle from './ThemeToggle.svelte';
import { initTheme, setThemeChoice } from '$lib/stores/theme';
import { THEME_KEY } from '$lib/theme';

let systemDark = false;
let listeners: (() => void)[] = [];

beforeEach(() => {
	localStorage.clear();
	document.documentElement.className = '';
	systemDark = false;
	listeners = [];
	vi.stubGlobal(
		'matchMedia',
		vi.fn(() => ({
			get matches() {
				return systemDark;
			},
			addEventListener: (_: string, fn: () => void) => listeners.push(fn),
			removeEventListener: (_: string, fn: () => void) => (listeners = listeners.filter((l) => l !== fn))
		}))
	);
	setThemeChoice('system');
});

afterEach(cleanup);

const isDark = () => document.documentElement.classList.contains('dark');

describe('ThemeToggle', () => {
	it('offers System, Light and Dark with System selected by default', () => {
		render(ThemeToggle);
		expect(screen.getByRole('radiogroup', { name: 'Colour theme' })).toBeInTheDocument();
		expect(screen.getByRole('radio', { name: 'System' })).toBeChecked();
		expect(screen.getByRole('radio', { name: 'Light' })).not.toBeChecked();
		expect(screen.getByRole('radio', { name: 'Dark' })).not.toBeChecked();
	});

	it('switches to dark, applies it and remembers it', async () => {
		render(ThemeToggle);
		await fireEvent.click(screen.getByRole('radio', { name: 'Dark' }));
		expect(screen.getByRole('radio', { name: 'Dark' })).toBeChecked();
		expect(isDark()).toBe(true);
		expect(localStorage.getItem(THEME_KEY)).toBe('dark');
	});

	it('forces light even when the OS prefers dark', async () => {
		systemDark = true;
		render(ThemeToggle);
		await fireEvent.click(screen.getByRole('radio', { name: 'System' }));
		expect(isDark()).toBe(true);
		await fireEvent.click(screen.getByRole('radio', { name: 'Light' }));
		expect(isDark()).toBe(false);
		expect(localStorage.getItem(THEME_KEY)).toBe('light');
	});

	it('going back to System clears the saved choice and follows the OS', async () => {
		systemDark = true;
		render(ThemeToggle);
		await fireEvent.click(screen.getByRole('radio', { name: 'Light' }));
		await fireEvent.click(screen.getByRole('radio', { name: 'System' }));
		expect(localStorage.getItem(THEME_KEY)).toBeNull();
		expect(isDark()).toBe(true);
	});
});

describe('initTheme', () => {
	it('restores the saved choice', () => {
		localStorage.setItem(THEME_KEY, 'dark');
		const stop = initTheme();
		render(ThemeToggle);
		expect(screen.getByRole('radio', { name: 'Dark' })).toBeChecked();
		expect(isDark()).toBe(true);
		stop();
	});

	it('follows OS changes only while the choice is System', () => {
		const stop = initTheme();
		expect(isDark()).toBe(false);
		systemDark = true;
		listeners.forEach((l) => l());
		expect(isDark()).toBe(true);

		setThemeChoice('light');
		listeners.forEach((l) => l());
		expect(isDark()).toBe(false);

		stop();
		expect(listeners).toHaveLength(0);
	});
});
