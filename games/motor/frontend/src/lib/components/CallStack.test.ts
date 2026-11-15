import { cleanup, render, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, it } from 'vitest';
import CallStack from './CallStack.svelte';
import type { Frame, GameMap, StepEvent } from '$lib/types';

afterEach(cleanup);

const map = (allowRecursion: boolean): Pick<GameMap, 'maxCallDepth' | 'allowRecursion'> => ({
	maxCallDepth: 16,
	allowRecursion
});

const stack: Frame[] = [
	{ tape: -1, pc: 1 },
	{ tape: 0, pc: 1 }
];

const event = (over: Partial<StepEvent> = {}): StepEvent => ({
	step: 1,
	instruction: 'CALL_SUB_1',
	tapeIndex: -1,
	slotIndex: 0,
	from: { x: 0, y: 0 },
	to: { x: 0, y: 0 },
	facingBefore: 'RIGHT',
	facingAfter: 'RIGHT',
	blocked: false,
	callBlocked: false,
	collected: null,
	callStack: stack,
	status: 'RUNNING',
	lossReason: null,
	...over
});

describe('CallStack', () => {
	it('states the recursion rule for both map kinds', () => {
		render(CallStack, { callStack: stack, map: map(false) });
		expect(screen.getByTestId('recursion-rule')).toHaveTextContent(/Recursion off/);
	});

	it('labels an allowing map as such', () => {
		render(CallStack, { callStack: stack, map: map(true) });
		expect(screen.getByTestId('recursion-rule')).toHaveTextContent(/Recursion allowed/);
	});

	it('explains an ignored call', () => {
		render(CallStack, { callStack: stack, map: map(false), lastEvent: event({ callBlocked: true }) });
		expect(screen.getByTestId('blocked-call')).toHaveTextContent(/ignored/);
	});

	it('stays quiet when no call was blocked', () => {
		render(CallStack, { callStack: stack, map: map(false), lastEvent: event() });
		expect(screen.queryByTestId('blocked-call')).toBeNull();
	});

	it('shows the depth against the limit', () => {
		render(CallStack, { callStack: stack, map: map(false) });
		expect(screen.getByTestId('depth')).toHaveTextContent('depth 1 / 16');
	});
});
