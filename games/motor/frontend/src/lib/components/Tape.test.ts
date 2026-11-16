import { cleanup, createEvent, fireEvent, render, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { activeDrag } from '../dnd';
import { emptyProgram, setSlot } from '../program';
import Tape from './Tape.svelte';

afterEach(() => {
	cleanup();
	activeDrag.set(null);
});

const cfg = { mainTapeLength: 3, subTapeLengths: [2] };

describe('Tape', () => {
	it('places the selected palette instruction when a slot is clicked', async () => {
		const onchange = vi.fn();
		render(Tape, { program: emptyProgram(cfg), selected: 'TURN_LEFT', onchange });
		await fireEvent.click(screen.getByTestId('slot--1-1'));
		expect(onchange.mock.calls[0][0].main).toEqual(['EMPTY', 'TURN_LEFT', 'EMPTY']);
	});

	it('clicking a slot with no tool armed opens an instruction menu', async () => {
		const onchange = vi.fn();
		render(Tape, { program: emptyProgram(cfg), onchange });
		await fireEvent.click(screen.getByTestId('slot--1-0'));
		const menu = screen.getByTestId('slot-picker');
		expect(menu.getAttribute('aria-label')).toBe('Main · slot 1');
		expect(screen.queryByTestId('pick-CALL_SUB_2')).toBeNull(); // only one sub
		expect(screen.queryByTestId('pick-EMPTY')).toBeNull(); // nothing to erase
		await fireEvent.click(screen.getByTestId('pick-TURN_RIGHT'));
		expect(onchange.mock.calls[0][0].main).toEqual(['TURN_RIGHT', 'EMPTY', 'EMPTY']);
		expect(screen.queryByTestId('slot-picker')).toBeNull();
		expect(document.activeElement).toBe(screen.getByTestId('slot--1-1'));
	});

	it('instruction menu: shortcut keys pick, Backspace erases, Escape closes', async () => {
		const onchange = vi.fn();
		render(Tape, { program: setSlot(emptyProgram(cfg), 0, 1, 'MOVE_FORWARD'), onchange });
		const slot = screen.getByTestId('slot-0-1');
		await fireEvent.click(slot);
		expect(document.activeElement).toBe(screen.getByTestId('pick-MOVE_FORWARD')); // current instruction focused
		await fireEvent.keyDown(document.activeElement!, { key: 'Escape' });
		expect(screen.queryByTestId('slot-picker')).toBeNull();
		expect(document.activeElement).toBe(slot);
		expect(onchange).not.toHaveBeenCalled();

		await fireEvent.click(slot);
		await fireEvent.keyDown(document.activeElement!, { key: 'Backspace' });
		expect(onchange.mock.calls[0][0].subs[0]).toEqual(['EMPTY', 'EMPTY']);

		await fireEvent.click(slot);
		await fireEvent.keyDown(document.activeElement!, { key: '1' });
		expect(onchange.mock.calls[1][0].subs[0][1]).toBe('CALL_SUB_1');
	});

	it('keyboard: F fills the focused slot, Backspace clears it, arrows move focus', async () => {
		const onchange = vi.fn();
		const { rerender } = render(Tape, { program: emptyProgram(cfg), onchange });
		const slot0 = screen.getByTestId('slot--1-0');
		slot0.focus();
		await fireEvent.keyDown(slot0, { key: 'f' });
		expect(onchange.mock.calls[0][0].main[0]).toBe('MOVE_FORWARD');
		expect(document.activeElement).toBe(screen.getByTestId('slot--1-1')); // advances after placing

		await fireEvent.keyDown(document.activeElement!, { key: 'ArrowDown' });
		expect(document.activeElement).toBe(screen.getByTestId('slot-0-1'));

		await rerender({ program: setSlot(emptyProgram(cfg), -1, 2, 'TURN_RIGHT'), onchange });
		const slot2 = screen.getByTestId('slot--1-2');
		await fireEvent.keyDown(slot2, { key: 'Backspace' });
		expect(onchange.mock.calls.at(-1)![0].main[2]).toBe('EMPTY');
	});

	it('call shortcut for a missing sub is ignored', async () => {
		const onchange = vi.fn();
		render(Tape, { program: emptyProgram(cfg), onchange });
		await fireEvent.keyDown(screen.getByTestId('slot--1-0'), { key: '2' });
		expect(onchange).not.toHaveBeenCalled();
	});

	it('clears every slot on every tape at once, without a dialog, keeping the tape shape', async () => {
		const onchange = vi.fn();
		let p = setSlot(setSlot(emptyProgram(cfg), -1, 0, 'MOVE_FORWARD'), 0, 1, 'TURN_LEFT');
		render(Tape, { program: p, onchange });

		const confirmSpy = vi.spyOn(window, 'confirm');
		await fireEvent.click(screen.getByTestId('clear-program'));
		expect(confirmSpy).not.toHaveBeenCalled(); // Undo is the safety net
		expect(onchange).toHaveBeenCalledOnce();
		expect(onchange.mock.calls[0][0]).toEqual({
			main: ['EMPTY', 'EMPTY', 'EMPTY'],
			subs: [['EMPTY', 'EMPTY']]
		});
		confirmSpy.mockRestore();
	});

	it('hides the clear-all control for an empty program and on read-only tapes', async () => {
		render(Tape, { program: emptyProgram(cfg), onchange: () => {} });
		expect(screen.queryByTestId('clear-program')).toBeNull();

		const filled = setSlot(emptyProgram(cfg), -1, 0, 'MOVE_FORWARD');
		await cleanup();
		render(Tape, { program: filled, disabled: true, onchange: () => {} });
		expect(screen.queryByTestId('clear-program')).toBeNull();
	});

	describe('slot usage and hints', () => {
		it('shows how many slots each tape uses', () => {
			let p = setSlot(setSlot(emptyProgram(cfg), -1, 0, 'MOVE_FORWARD'), -1, 2, 'TURN_LEFT');
			p = setSlot(p, 0, 1, 'CALL_SUB_1');
			render(Tape, { program: p, onchange: () => {} });
			expect(screen.getByTestId('slot-usage--1').textContent).toBe('(2 of 3 slots used)');
			expect(screen.getByTestId('slot-usage-0').textContent).toBe('(1 of 2 slots used)');
		});

		it('flags a filled sub that nothing calls, and clears the flag once main calls it', async () => {
			const filledSub = setSlot(emptyProgram(cfg), 0, 0, 'MOVE_FORWARD');
			const { rerender } = render(Tape, { program: filledSub, onchange: () => {} });
			expect(screen.getByTestId('never-called-0').textContent).toMatch(/never called/i);
			await rerender({ program: setSlot(filledSub, -1, 0, 'CALL_SUB_1'), onchange: () => {} });
			expect(screen.queryByTestId('never-called-0')).toBeNull();
		});

		it('does not flag an empty sub', () => {
			render(Tape, { program: emptyProgram(cfg), onchange: () => {} });
			expect(screen.queryByTestId('never-called-0')).toBeNull();
		});
	});

	describe('undo and redo buttons', () => {
		const filled = () => setSlot(emptyProgram(cfg), -1, 0, 'MOVE_FORWARD');

		it('are present on an empty program so the first edit does not shift the slots', () => {
			render(Tape, { program: emptyProgram(cfg), onchange: () => {} });
			expect((screen.getByTestId('undo') as HTMLButtonElement).disabled).toBe(true);
			expect((screen.getByTestId('redo') as HTMLButtonElement).disabled).toBe(true);
		});

		it('are disabled until there is something to step through, then call the handlers', async () => {
			const onundo = vi.fn();
			const onredo = vi.fn();
			const { rerender } = render(Tape, { program: filled(), onchange: () => {}, onundo, onredo });
			expect((screen.getByTestId('undo') as HTMLButtonElement).disabled).toBe(true);
			expect((screen.getByTestId('redo') as HTMLButtonElement).disabled).toBe(true);

			await rerender({ program: filled(), onchange: () => {}, onundo, onredo, canUndo: true, canRedo: true });
			await fireEvent.click(screen.getByTestId('undo'));
			await fireEvent.click(screen.getByTestId('redo'));
			expect(onundo).toHaveBeenCalledOnce();
			expect(onredo).toHaveBeenCalledOnce();
		});

		it('stay reachable after Clear all empties the program, and hide on read-only tapes', async () => {
			render(Tape, { program: emptyProgram(cfg), onchange: () => {}, canUndo: true });
			expect((screen.getByTestId('undo') as HTMLButtonElement).disabled).toBe(false);
			expect(screen.queryByTestId('clear-program')).toBeNull();

			await cleanup();
			render(Tape, { program: filled(), disabled: true, canUndo: true, onchange: () => {} });
			expect(screen.queryByTestId('undo')).toBeNull();
		});
	});

	describe('drag and drop', () => {
		/** Minimal DataTransfer: jsdom doesn't implement one. */
		function transfer() {
			const data = new Map<string, string>();
			return {
				setData: (k: string, v: string) => void data.set(k, v),
				getData: (k: string) => data.get(k) ?? '',
				setDragImage: vi.fn(),
				effectAllowed: '',
				dropEffect: ''
			};
		}

		/** jsdom has no DragEvent, so modifier keys in the init dict are dropped; set them on the event. */
		async function fireWith(type: 'dragOver' | 'drop', el: HTMLElement, dataTransfer: unknown, mods: Record<string, boolean>) {
			const ev = createEvent[type](el, { dataTransfer });
			for (const [k, v] of Object.entries(mods)) Object.defineProperty(ev, k, { value: v });
			await fireEvent(el, ev);
		}

		const filled = () => {
			let p = emptyProgram(cfg);
			(['MOVE_FORWARD', 'TURN_LEFT', 'TURN_RIGHT'] as const).forEach((ins, i) => (p = setSlot(p, -1, i, ins)));
			return p;
		};

		it('swaps on a plain drop, using a compact tile as the drag image', async () => {
			const onchange = vi.fn();
			render(Tape, { program: filled(), onchange });
			const dt = transfer();
			await fireEvent.dragStart(screen.getByTestId('slot--1-0'), { dataTransfer: dt });
			expect(dt.setDragImage).toHaveBeenCalledOnce();
			await fireEvent.dragOver(screen.getByTestId('slot--1-2'), { dataTransfer: dt });
			await fireEvent.drop(screen.getByTestId('slot--1-2'), { dataTransfer: dt });
			expect(onchange.mock.calls[0][0].main).toEqual(['TURN_RIGHT', 'TURN_LEFT', 'MOVE_FORWARD']);
		});

		it('Shift inserts: reorders within the tape and shows the insert marker', async () => {
			const onchange = vi.fn();
			render(Tape, { program: filled(), onchange });
			const dt = transfer();
			await fireEvent.dragStart(screen.getByTestId('slot--1-0'), { dataTransfer: dt });
			await fireWith('dragOver', screen.getByTestId('slot--1-2'), dt, { shiftKey: true });
			expect(screen.getByTestId('insert-marker')).toBeTruthy();
			expect(dt.dropEffect).toBe('move');
			await fireWith('drop', screen.getByTestId('slot--1-2'), dt, { shiftKey: true });
			expect(onchange.mock.calls[0][0].main).toEqual(['TURN_LEFT', 'TURN_RIGHT', 'MOVE_FORWARD']);
			expect(screen.queryByTestId('insert-marker')).toBeNull();
		});

		it('Shift across tapes previews and performs a swap, not an insert', async () => {
			const onchange = vi.fn();
			render(Tape, { program: filled(), onchange });
			const dt = transfer();
			await fireEvent.dragStart(screen.getByTestId('slot--1-0'), { dataTransfer: dt });
			await fireWith('dragOver', screen.getByTestId('slot-0-0'), dt, { shiftKey: true });
			expect(screen.queryByTestId('insert-marker')).toBeNull();
			await fireWith('drop', screen.getByTestId('slot-0-0'), dt, { shiftKey: true });
			const out = onchange.mock.calls[0][0];
			expect(out.main[0]).toBe('EMPTY');
			expect(out.subs[0][0]).toBe('MOVE_FORWARD');
		});

		it('Alt copies and signals the copy cursor', async () => {
			const onchange = vi.fn();
			render(Tape, { program: filled(), onchange });
			const dt = transfer();
			await fireEvent.dragStart(screen.getByTestId('slot--1-0'), { dataTransfer: dt });
			await fireWith('dragOver', screen.getByTestId('slot-0-1'), dt, { altKey: true });
			expect(dt.dropEffect).toBe('copy');
			await fireWith('drop', screen.getByTestId('slot-0-1'), dt, { altKey: true });
			const out = onchange.mock.calls[0][0];
			expect(out.main[0]).toBe('MOVE_FORWARD');
			expect(out.subs[0][1]).toBe('MOVE_FORWARD');
		});

		it('ignores foreign drags (no active drag) and read-only tapes', async () => {
			const onchange = vi.fn();
			render(Tape, { program: filled(), onchange });
			const dt = transfer();
			const over = await fireEvent.dragOver(screen.getByTestId('slot--1-1'), { dataTransfer: dt });
			expect(over).toBe(true); // not cancelled, so the browser keeps its default handling

			await cleanup();
			render(Tape, { program: filled(), disabled: true, onchange });
			await fireEvent.dragStart(screen.getByTestId('slot--1-0'), { dataTransfer: dt });
			await fireEvent.drop(screen.getByTestId('slot--1-1'), { dataTransfer: dt });
			expect(onchange).not.toHaveBeenCalled();
		});

		it('clears the drop highlight when the drag ends without a drop', async () => {
			render(Tape, { program: filled(), onchange: () => {} });
			const dt = transfer();
			const source = screen.getByTestId('slot--1-0');
			await fireEvent.dragStart(source, { dataTransfer: dt });
			const target = screen.getByTestId('slot--1-1');
			await fireEvent.dragOver(target, { dataTransfer: dt });
			expect(target.className).toMatch(/outline-dashed/);
			await fireEvent.dragEnd(source, { dataTransfer: dt });
			expect(target.className).not.toMatch(/outline-dashed/);
		});
	});

	it('read-only tapes ignore edits but still allow navigation, and expose highlights to AT', async () => {		const onchange = vi.fn();
		render(Tape, {
			program: emptyProgram(cfg),
			disabled: true,
			selected: 'MOVE_FORWARD',
			callStack: [
				{ tape: -1, pc: 2 },
				{ tape: 0, pc: 1 }
			],
			onchange
		});
		await fireEvent.click(screen.getByTestId('slot--1-0'));
		await fireEvent.keyDown(screen.getByTestId('slot--1-0'), { key: 'f' });
		expect(onchange).not.toHaveBeenCalled();
		expect(screen.queryByTestId('slot-picker')).toBeNull();
		expect(screen.getByTestId('slot--1-1').getAttribute('aria-label')).toMatch(/waiting for subroutine/);
		expect(screen.getByTestId('slot-0-1').getAttribute('aria-label')).toMatch(/next to run/);
	});
});
