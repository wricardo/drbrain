<script lang="ts">
	import { DRAG_MIME, activeDrag, decodeDrag, dropEffectFor, dropModeFor, encodeDrag } from '$lib/dnd';
	import { setTileDragImage } from '$lib/dragImage';
	import { computeSlotMarks, marksFor, type SlotMarks } from '$lib/highlight';
	import { instructionMeta, tapeLabel } from '$lib/instructions';
	import {
		applyDrop,
		clearProgram,
		hasInstructions,
		setSlot,
		tapeLengths,
		tapeOf,
		unreachableSubs,
		usedSlots,
		type DragSource,
		type DropMode
	} from '$lib/program';
	import { keyToAction, moveSelection } from '$lib/shortcuts';
	import SlotPicker from './SlotPicker.svelte';
	import type { Frame, Instruction, Program, Status, StepEvent } from '$lib/types';

	let {
		program,
		selected = null,
		disabled = false,
		callStack = [],
		lastEvent = null,
		status = 'READY',
		onchange = () => {},
		canUndo = false,
		canRedo = false,
		onundo = () => {},
		onredo = () => {}
	}: {
		program: Program;
		/** Palette instruction placed on slot click. */
		selected?: Instruction | null;
		/** Read-only: no edits, drag or shortcuts (highlights still show). */
		disabled?: boolean;
		callStack?: Frame[];
		lastEvent?: StepEvent | null;
		status?: Status;
		onchange?: (p: Program) => void;
		/** Undo/redo availability and handlers; each button is disabled until there is something to step through. */
		canUndo?: boolean;
		canRedo?: boolean;
		onundo?: () => void;
		onredo?: () => void;
	} = $props();

	// Filled subs nothing calls: they sit in the program but never run.
	const neverCalled = $derived(new Set(unreachableSubs(program)));

	let root: HTMLElement | undefined = $state();
	let dropTarget = $state<string | null>(null);
	let dropMode = $state<DropMode>('move');
	// Slot being dragged ("tape:slot"), dimmed while the drag is in flight.
	let dragging = $state<string | null>(null);

	/**
	 * Which edge of the target the insert bar sits on: a slot dragged forward lands
	 * after the target, one dragged back lands before it (matches insertSlot).
	 */
	function insertSide(tape: number, slot: number): 'before' | 'after' {
		const s = $activeDrag;
		return s?.kind === 'slot' && s.tape === tape && s.slot < slot ? 'after' : 'before';
	}
	// Slot whose instruction menu is open (click with no palette tool armed).
	let picker = $state<{ tape: number; slot: number; anchor: HTMLElement } | null>(null);
	$effect(() => {
		if (disabled) picker = null;
	});

	const tapes = $derived([
		{ tape: -1, slots: program.main },
		...program.subs.map((slots, i) => ({ tape: i, slots }))
	]);
	const lengthsByTape = $derived(
		Object.fromEntries([-1, ...program.subs.keys()].map((t, i) => [t, tapeLengths(program)[i]]))
	);
	const marks = $derived(computeSlotMarks(callStack, lastEvent, status, lengthsByTape));

	function focusSlot(tape: number, slot: number) {
		root?.querySelector<HTMLElement>(`[data-slot="${tape}:${slot}"]`)?.focus();
	}

	function onKeyDown(e: KeyboardEvent, tape: number, slot: number) {
		if (e.ctrlKey || e.metaKey || e.altKey) return;
		const action = keyToAction(e.key, program.subs.length);
		if (!action) return;
		if (action.type === 'move') {
			e.preventDefault();
			const next = moveSelection(tapeLengths(program), { tape, slot }, action.dx, action.dy);
			focusSlot(next.tape, next.slot);
			return;
		}
		if (disabled) return;
		e.preventDefault();
		if (action.type === 'clear') {
			onchange(setSlot(program, tape, slot, 'EMPTY'));
			return;
		}
		onchange(setSlot(program, tape, slot, action.instruction));
		// Advance so a program can be typed in one go.
		focusSlot(tape, slot + 1);
	}

	function onClick(e: MouseEvent, tape: number, slot: number) {
		if (disabled) return;
		if (selected !== null) {
			onchange(setSlot(program, tape, slot, selected));
			return;
		}
		const same = picker?.tape === tape && picker.slot === slot;
		picker = same ? null : { tape, slot, anchor: e.currentTarget as HTMLElement };
	}

	function closePicker(restoreFocus = true) {
		if (!picker) return;
		const { tape, slot } = picker;
		picker = null;
		if (restoreFocus) focusSlot(tape, slot);
	}

	function pick(ins: Instruction) {
		if (!picker) return;
		const { tape, slot } = picker;
		picker = null;
		onchange(setSlot(program, tape, slot, ins));
		// Like typing a shortcut: advance so the next slot is one Enter away.
		focusSlot(tape, ins === 'EMPTY' ? slot : Math.min(slot + 1, tapeOf(program, tape)!.length - 1));
	}

	function onDragStart(e: DragEvent, tape: number, slot: number, ins: Instruction) {
		if (disabled || !e.dataTransfer) {
			e.preventDefault();
			return;
		}
		const source: DragSource = { kind: 'slot', tape, slot };
		e.dataTransfer.setData(DRAG_MIME, encodeDrag(source));
		e.dataTransfer.effectAllowed = 'copyMove';
		const meta = instructionMeta(ins);
		setTileDragImage(e, meta.glyph, meta.classes);
		activeDrag.set(source);
		// Dim the source after the browser has snapshotted the drag image.
		setTimeout(() => (dragging = `${tape}:${slot}`), 0);
	}

	function onDragOver(e: DragEvent, tape: number, slot: number) {
		if (disabled) return;
		const source = $activeDrag;
		// Not one of our drags (a file, selected text): leave it to the browser.
		if (!source) return;
		e.preventDefault();
		dropTarget = `${tape}:${slot}`;
		dropMode = dropModeFor(source, e);
		// Reordering across tapes can't shift anything, so the preview shows the swap that will happen.
		if (dropMode === 'insert' && source.kind === 'slot' && source.tape !== tape) dropMode = 'move';
		if (e.dataTransfer) e.dataTransfer.dropEffect = dropEffectFor(dropMode);
	}

	function onDragLeave(e: DragEvent) {
		// dragleave also fires when the pointer moves onto a child element; ignore that.
		if (e.currentTarget instanceof Node && e.relatedTarget instanceof Node && e.currentTarget.contains(e.relatedTarget)) return;
		dropTarget = null;
	}

	function endDrag() {
		dropTarget = null;
		dragging = null;
		activeDrag.set(null);
	}

	function onDrop(e: DragEvent, tape: number, slot: number) {
		// The drag is over either way; don't rely on dragend reaching a source that may have re-rendered.
		endDrag();
		if (disabled) return;
		e.preventDefault();
		const source = decodeDrag(e.dataTransfer?.getData(DRAG_MIME) ?? '');
		if (!source) return;
		onchange(applyDrop(program, source, { tape, slot }, dropModeFor(source, e)));
	}

	function slotClasses(ins: Instruction, m: SlotMarks): string {
		const parts = [instructionMeta(ins).classes];
		if (m.active) parts.push('ring-4 ring-indigo-500 z-10');
		if (m.caller) parts.push('ring-2 ring-violet-600 ring-offset-2');
		if (m.executed) parts.push('inset-ring-4 inset-ring-yellow-400');
		return parts.join(' ');
	}

	/** Drop-target styling: indigo for a swap, emerald for a copy; an insert gets a bar instead (see markup). */
	function dropClasses(key: string): string {
		if (dropTarget !== key) return dragging === key ? 'opacity-40' : '';
		if (dropMode === 'insert') return 'scale-105';
		const tone = dropMode === 'copy' ? 'outline-emerald-600 ring-emerald-300' : 'outline-indigo-600 ring-indigo-300';
		return `z-10 scale-110 shadow-lg outline-2 outline-dashed ring-4 ${tone}`;
	}

	function slotLabel(tape: number, slot: number, ins: Instruction, m: SlotMarks): string {
		const parts = [`${tapeLabel(tape)} slot ${slot + 1}: ${instructionMeta(ins).label}`];
		if (m.active) parts.push('next to run');
		if (m.caller) parts.push('waiting for subroutine to return');
		if (m.executed) parts.push('just executed');
		return parts.join(', ');
	}

	const historyBtn =
		'rounded-lg border border-indigo-300 px-2.5 py-1 text-xs font-semibold text-indigo-900 hover:bg-indigo-50 disabled:cursor-not-allowed disabled:opacity-40 disabled:hover:bg-transparent';

	/** Empties every slot on every tape (the tape lengths stay the same). No confirmation: Undo restores it. */
	function clearAll() {
		if (disabled) return;
		picker = null;
		onchange(clearProgram(program));
	}
</script>

<div bind:this={root} class="space-y-3" role="group" aria-label="Program tapes">
	{#if !disabled && (hasInstructions(program) || canUndo || canRedo)}
		<div class="flex items-center justify-between gap-2">
			<div class="flex gap-1.5">
				<button
					type="button"
					data-testid="undo"
					class={historyBtn}
					disabled={!canUndo}
					title="Undo (Ctrl+Z)"
					onclick={onundo}>↶ Undo</button
				>
				<button
					type="button"
					data-testid="redo"
					class={historyBtn}
					disabled={!canRedo}
					title="Redo (Ctrl+Shift+Z)"
					onclick={onredo}>↷ Redo</button
				>
			</div>
			{#if hasInstructions(program)}
				<button
					type="button"
					data-testid="clear-program"
					class="rounded-lg border border-rose-600 bg-rose-600 px-2.5 py-1 text-xs font-semibold text-white hover:bg-rose-700 hover:border-rose-700"
					onclick={clearAll}>Clear all slots</button
				>
			{/if}
		</div>
	{/if}
	{#each tapes as t (t.tape)}
		<div>
			<h3 class="text-xs font-semibold uppercase tracking-wide text-indigo-800 mb-1">
				{tapeLabel(t.tape)}
				<span class="font-normal text-slate-500" data-testid="slot-usage-{t.tape}">({usedSlots(t.slots)} of {t.slots.length} slots used)</span>
				{#if t.tape >= 0 && neverCalled.has(t.tape)}
					<span class="ml-1 rounded bg-amber-100 px-1.5 py-0.5 font-medium normal-case tracking-normal text-amber-900" data-testid="never-called-{t.tape}"
						>Never called, so it won't run</span
					>
				{/if}
			</h3>
			<div class="flex flex-wrap gap-1.5">
				{#each t.slots as ins, slot (slot)}
					{@const m = marksFor(marks, t.tape, slot)}
					<button
						type="button"
						data-slot="{t.tape}:{slot}"
						data-testid="slot-{t.tape}-{slot}"
						class="relative h-12 w-12 rounded-md border-2 text-base font-bold transition-transform duration-100 {slotClasses(ins, m)} {dropClasses(
							`${t.tape}:${slot}`
						)} {disabled ? 'cursor-default' : 'cursor-pointer'}"
						aria-label={slotLabel(t.tape, slot, ins, m)}
						aria-disabled={disabled}
						aria-haspopup={!disabled && selected === null ? 'menu' : undefined}
						aria-expanded={picker?.tape === t.tape && picker.slot === slot ? true : undefined}
						draggable={!disabled && ins !== 'EMPTY'}
						onclick={(e) => onClick(e, t.tape, slot)}
						onkeydown={(e) => onKeyDown(e, t.tape, slot)}
						ondragstart={(e) => onDragStart(e, t.tape, slot, ins)}
						ondragover={(e) => onDragOver(e, t.tape, slot)}
						ondragleave={onDragLeave}
						ondrop={(e) => onDrop(e, t.tape, slot)}
						ondragend={endDrag}
					>
						{#if dropTarget === `${t.tape}:${slot}` && dropMode === 'insert'}
							<span
								data-testid="insert-marker"
								class="pointer-events-none absolute -top-1 -bottom-1 w-1.5 rounded-full bg-indigo-600 {insertSide(t.tape, slot) === 'before' ? '-left-1.5' : '-right-1.5'}"
								aria-hidden="true"
							></span>
						{/if}
						<span class="pointer-events-none" aria-hidden="true">{instructionMeta(ins).glyph}</span>
						<span class="pointer-events-none absolute bottom-0 right-0.5 text-[9px] font-normal opacity-60" aria-hidden="true">{slot + 1}</span>
					</button>
				{/each}
			</div>
		</div>
	{/each}
</div>

{#if picker}
	<SlotPicker
		anchor={picker.anchor}
		title="{tapeLabel(picker.tape)} · slot {picker.slot + 1}"
		current={tapeOf(program, picker.tape)?.[picker.slot] ?? 'EMPTY'}
		subCount={program.subs.length}
		onpick={pick}
		onclose={closePicker}
	/>
{/if}
