import { describe, expect, it } from 'vitest';
import { editKeyAction, globalKeyAction } from './globalKeys';

function ev(key: string, target: Element | null = document.body, mods: Partial<KeyboardEvent> = {}) {
	return { key, target, ctrlKey: false, metaKey: false, altKey: false, shiftKey: false, ...mods } as KeyboardEvent;
}

describe('globalKeyAction', () => {
	it('maps R to reset and ? to shortcuts on the page', () => {
		expect(globalKeyAction(ev('r'))).toBe('reset');
		expect(globalKeyAction(ev('R'))).toBe('reset');
		expect(globalKeyAction(ev('?'))).toBe('shortcuts');
		expect(globalKeyAction(ev('s'))).toBe('step');
		expect(globalKeyAction(ev('S'))).toBe('step');
		expect(globalKeyAction(ev('b'))).toBe('back');
		expect(globalKeyAction(ev('B'))).toBe('back');
		expect(globalKeyAction(ev('x'))).toBeNull();
	});

	it('ignores keys in program slots, form fields and with modifiers', () => {
		const slot = document.createElement('button');
		slot.dataset.slot = '-1:0';
		const input = document.createElement('input');
		expect(globalKeyAction(ev('r', slot))).toBeNull();
		expect(globalKeyAction(ev('r', input))).toBeNull();
		expect(globalKeyAction(ev('s', slot))).toBeNull();
		expect(globalKeyAction(ev('b', slot))).toBeNull();
		expect(globalKeyAction(ev('r', document.body, { metaKey: true }))).toBeNull();
	});
});

describe('editKeyAction', () => {
	it('maps Ctrl/Cmd+Z to undo, +Shift+Z and Ctrl+Y to redo, Ctrl/Cmd+Enter to run', () => {
		expect(editKeyAction(ev('z', document.body, { ctrlKey: true }))).toBe('undo');
		expect(editKeyAction(ev('Z', document.body, { metaKey: true }))).toBe('undo');
		expect(editKeyAction(ev('Z', document.body, { ctrlKey: true, shiftKey: true }))).toBe('redo');
		expect(editKeyAction(ev('z', document.body, { metaKey: true, shiftKey: true }))).toBe('redo');
		expect(editKeyAction(ev('y', document.body, { ctrlKey: true }))).toBe('redo');
		expect(editKeyAction(ev('Enter', document.body, { ctrlKey: true }))).toBe('run');
		expect(editKeyAction(ev('Enter', document.body, { metaKey: true }))).toBe('run');
	});

	it('needs Ctrl or Cmd, and ignores Alt combos and other keys', () => {
		expect(editKeyAction(ev('z'))).toBeNull();
		expect(editKeyAction(ev('Enter'))).toBeNull();
		expect(editKeyAction(ev('z', document.body, { ctrlKey: true, altKey: true }))).toBeNull();
		expect(editKeyAction(ev('x', document.body, { ctrlKey: true }))).toBeNull();
	});

	it('works from a focused slot but leaves form fields their native undo', () => {
		const slot = document.createElement('button');
		slot.dataset.slot = '-1:0';
		const input = document.createElement('input');
		expect(editKeyAction(ev('z', slot, { ctrlKey: true }))).toBe('undo');
		expect(editKeyAction(ev('z', input, { ctrlKey: true }))).toBeNull();
	});
});
