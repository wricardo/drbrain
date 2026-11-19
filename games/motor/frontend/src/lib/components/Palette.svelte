<script lang="ts">
	import { DRAG_MIME, activeDrag, decodeDrag, encodeDrag, eraseTargetFor } from '$lib/dnd';
	import { setTileDragImage } from '$lib/dragImage';
	import { instructionMeta, paletteInstructions } from '$lib/instructions';
	import type { SlotRef } from '$lib/program';
	import type { Instruction } from '$lib/types';

	let {
		selected = $bindable(null),
		subCount,
		disabled = false,
		onerase = () => {}
	}: {
		selected?: Instruction | null;
		subCount: number;
		disabled?: boolean;
		/** A placed step was dropped on the palette (the Erase tile or anywhere else in it). */
		onerase?: (slot: SlotRef) => void;
	} = $props();

	const items = $derived<Instruction[]>([...paletteInstructions(subCount), 'EMPTY']);

	// True while a placed step is dragged over the palette.
	let eraseHover = $state(false);
	// The palette is an erase drop zone only for slot drags (a palette tile dropped here does nothing).
	const slotDragActive = $derived(eraseTargetFor($activeDrag, disabled) !== null);

	function toggle(ins: Instruction) {
		if (disabled) return;
		selected = selected === ins ? null : ins;
	}

	function onDragStart(e: DragEvent, ins: Instruction) {
		if (disabled || !e.dataTransfer) {
			e.preventDefault();
			return;
		}
		const source = { kind: 'palette', instruction: ins } as const;
		e.dataTransfer.setData(DRAG_MIME, encodeDrag(source));
		e.dataTransfer.effectAllowed = 'copy';
		const meta = instructionMeta(ins);
		setTileDragImage(e, ins === 'EMPTY' ? '⌫' : meta.glyph, meta.classes);
		activeDrag.set(source);
	}

	function onZoneDragOver(e: DragEvent) {
		if (!slotDragActive) return;
		e.preventDefault();
		eraseHover = true;
		if (e.dataTransfer) e.dataTransfer.dropEffect = 'move';
	}

	function onZoneDragLeave(e: DragEvent) {
		if (e.currentTarget instanceof Node && e.relatedTarget instanceof Node && e.currentTarget.contains(e.relatedTarget)) return;
		eraseHover = false;
	}

	function onZoneDrop(e: DragEvent) {
		const wasSlotDrag = slotDragActive;
		endDrag();
		if (!wasSlotDrag) return;
		e.preventDefault();
		const target = eraseTargetFor(decodeDrag(e.dataTransfer?.getData(DRAG_MIME) ?? ''), disabled);
		if (target) onerase(target);
	}

	function endDrag() {
		eraseHover = false;
		activeDrag.set(null);
	}
</script>

<svelte:window onkeydown={(e) => e.key === 'Escape' && selected !== null && (selected = null)} />

<!-- The whole palette is an erase drop zone while a placed step is dragged (plain drop target, not a control). -->
<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
<div
	role="group"
	aria-label="Instruction palette"
	data-testid="palette"
	class="relative flex flex-wrap gap-2 rounded-lg transition-all duration-100 {slotDragActive
		? 'outline-2 outline-dashed outline-offset-4 outline-rose-500'
		: ''} {slotDragActive && eraseHover ? 'bg-rose-50 ring-4 ring-rose-300 outline-rose-600' : ''}"
	ondragover={onZoneDragOver}
	ondragleave={onZoneDragLeave}
	ondrop={onZoneDrop}
>
	{#each items as ins (ins)}
		{@const meta = instructionMeta(ins)}
		{@const erase = ins === 'EMPTY'}
		<button
			type="button"
			data-testid={erase ? 'palette-erase' : undefined}
			class="min-w-14 rounded-lg border-2 px-2 py-1.5 text-sm font-semibold transition-all duration-100 sm:min-w-16 sm:px-3 sm:py-2 {meta.classes} {selected === ins
				? 'ring-4 ring-indigo-500'
				: ''} {disabled ? 'opacity-50 cursor-not-allowed' : 'hover:brightness-95 cursor-pointer'} {erase && slotDragActive
				? 'outline-2 outline-dashed outline-rose-500'
				: ''} {erase && eraseHover ? 'scale-110 shadow-lg ring-4 ring-rose-300 outline-rose-600' : ''}"
			aria-pressed={selected === ins}
			aria-disabled={disabled}
			draggable={!disabled}
			ondragstart={(e) => onDragStart(e, ins)}
			ondragend={endDrag}
			onclick={() => toggle(ins)}
			title={slotDragActive ? 'Drop here to erase the slot' : `${meta.label}${meta.key ? ` (${meta.key})` : ' (Backspace)'}`}
		>
			<span class="pointer-events-none block text-lg leading-none" aria-hidden="true">{erase ? '⌫' : meta.glyph}</span>
			<span class="pointer-events-none block text-[11px] mt-1">{erase ? 'Erase' : meta.label}</span>
			{#if meta.key}<kbd class="pointer-events-none block text-[10px] opacity-70 max-sm:hidden">{meta.key}</kbd>{/if}
		</button>
	{/each}
	{#if slotDragActive}
		<span
			data-testid="palette-erase-hint"
			class="pointer-events-none absolute -top-3 right-2 rounded-full px-2 text-[11px] font-semibold {eraseHover
				? 'bg-rose-600 text-white'
				: 'bg-rose-100 text-rose-800'}"
		>
			⌫ Drop here to erase
		</span>
	{/if}
</div>
<p class="min-h-8 text-xs text-slate-600" role="status" data-testid="palette-hint">
	{#if selected !== null}
		Stamping <strong>{selected === 'EMPTY' ? 'Erase' : instructionMeta(selected).label}</strong>: click slots to place it. Press Esc or click the tile again to stop.
	{:else}
		Click a slot to choose, drag a tile onto a slot, or click a tile to stamp it into several slots.
	{/if}
</p>
