import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import ShortcutsDialog from './ShortcutsDialog.svelte';

afterEach(cleanup);

/** Mirrors mazes' SHORTCUTS: two rows deliberately share the 'Move one tile' action. */
const items = [
	{ keys: ['← ↑ ↓ →'], action: 'Move one tile' },
	{ keys: ['W A S D'], action: 'Move one tile' },
	{ keys: ['?'], action: 'Show these shortcuts' }
];

describe('ShortcutsDialog', () => {
	it('renders rows whose actions repeat', () => {
		render(ShortcutsDialog, { items, onclose: vi.fn() });
		expect(screen.getAllByText('Move one tile')).toHaveLength(2);
		expect(screen.getByText('W A S D')).toBeTruthy();
	});

	it('closes on Escape and, when asked, on ?', async () => {
		const onclose = vi.fn();
		render(ShortcutsDialog, { items, closeOnQuestion: true, onclose });
		await fireEvent.keyDown(window, { key: '?' });
		await fireEvent.keyDown(window, { key: 'Escape' });
		expect(onclose).toHaveBeenCalledTimes(2);
	});
});
