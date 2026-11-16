<script lang="ts">
	import { DRAG_MIME, activeDrag, decodeDrag, encodeDrag } from '$lib/dnd';
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
		/** A slot was dropped on the Erase tile. */
		onerase?: (slot: SlotRef) => void;
	} = $props();

	const items = $derived<Instruction[]>([...paletteInstructions(subCount), 'EMPTY']);

	// True while a slot is dragged over the Erase tile.
	let eraseHover = $state(false);
	// Erase is a drop target only for slot drags (a palette tile dropped here would do nothing).
	const slotDragActive = $derived(!disabled && $activeDrag?.kind === 'slot');

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

	function onEraseDragOver(e: DragEvent) {
		if (!slotDragActive) return;
		e.preventDefault();
		eraseHover = true;
		if (e.dataTransfer) e.dataTransfer.dropEffect = 'move';
	}

	function onEraseDragLeave(e: DragEvent) {
		if (e.currentTarget instanceof Node && e.relatedTarget instanceof Node && e.currentTarget.contains(e.relatedTarget)) return;
		eraseHover = false;
	}

	function onEraseDrop(e: DragEvent) {
		const wasSlotDrag = slotDragActive;
		endDrag();
		if (!wasSlotDrag) return;
		e.preventDefault();
		const source = decodeDrag(e.dataTransfer?.getData(DRAG_MIME) ?? '');
		if (source?.kind === 'slot') onerase({ tape: source.tape, slot: source.slot });
	}

	function endDrag() {
		eraseHover = false;
		activeDrag.set(null);
	}
</script>

<svelte:window onkeydown={(e) => e.key === 'Escape' && selected !== null && (selected = null)} />

<div role="group" aria-label="Instruction palette" class="flex flex-wrap gap-2">
	{#each items as ins (ins)}
		{@const meta = instructionMeta(ins)}
		{@const erase = ins === 'EMPTY'}
		<button
			type="button"
			data-testid={erase ? 'palette-erase' : undefined}
			class="min-w-16 rounded-lg border-2 px-3 py-2 text-sm font-semibold transition-all duration-100 {meta.classes} {selected === ins
				? 'ring-4 ring-indigo-500'
				: ''} {disabled ? 'opacity-50 cursor-not-allowed' : 'hover:brightness-95 cursor-pointer'} {erase && slotDragActive
				? 'outline-2 outline-dashed outline-rose-500'
				: ''} {erase && eraseHover ? 'scale-110 shadow-lg ring-4 ring-rose-300 outline-rose-600' : ''}"
			aria-pressed={selected === ins}
			aria-disabled={disabled}
			draggable={!disabled}
			ondragstart={(e) => onDragStart(e, ins)}
			ondragend={endDrag}
			ondragover={erase ? onEraseDragOver : undefined}
			ondragleave={erase ? onEraseDragLeave : undefined}
			ondrop={erase ? onEraseDrop : undefined}
			onclick={() => toggle(ins)}
			title={erase && slotDragActive ? 'Drop here to erase the slot' : `${meta.label}${meta.key ? ` (${meta.key})` : ' (Backspace)'}`}
		>
			<span class="pointer-events-none block text-lg leading-none" aria-hidden="true">{erase ? '⌫' : meta.glyph}</span>
			<span class="pointer-events-none block text-[11px] mt-1">{erase ? 'Erase' : meta.label}</span>
			{#if meta.key}<kbd class="pointer-events-none block text-[10px] opacity-70">{meta.key}</kbd>{/if}
		</button>
	{/each}
</div>
<p class="min-h-8 text-xs text-slate-600" role="status" data-testid="palette-hint">
	{#if selected !== null}
		Stamping <strong>{selected === 'EMPTY' ? 'Erase' : instructionMeta(selected).label}</strong>: click slots to place it. Press Esc or click the tile again to stop.
	{:else}
		Click a slot to choose, drag a tile onto a slot, or click a tile to stamp it into several slots.
	{/if}
</p>
