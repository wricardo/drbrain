<script lang="ts">
	import { parseLayout } from '$lib/layout';
	import { lockColor, lockInk } from '$lib/tiles';

	/** fit: scale to fit inside a square parent (width as a share of the parent) instead of capping at 200px. */
	let { layout, label = '', fit = false }: { layout: string[]; label?: string; fit?: boolean } = $props();

	const parsed = $derived(parseLayout(layout));
	const arrow = $derived(parsed.start ? { UP: '▲', RIGHT: '▶', DOWN: '▼', LEFT: '◀' }[parsed.start.facing] : '');

	/**
	 * Special tiles the preview paints, using the board's colour language (Grid.svelte) so a
	 * thumbnail reads at a glance: conveyors and mirrors get a glyph on a tint of their colour,
	 * keys and doors a solid block in it. Layout is the only source of tiles, so parsing the
	 * layout is enough — no map fields needed.
	 */
	type Look = { kind: string; glyph: string; solid?: boolean };
	const TILE_LOOK: Record<string, Look> = {
		U: { kind: 'conveyor', glyph: '▲' },
		D: { kind: 'conveyor', glyph: '▼' },
		L: { kind: 'conveyor', glyph: '◀' },
		R: { kind: 'conveyor', glyph: '▶' },
		'/': { kind: 'mirror', glyph: '/' },
		'\\': { kind: 'mirror', glyph: '\\' },
		a: { kind: 'key', glyph: 'a', solid: true },
		b: { kind: 'key', glyph: 'b', solid: true },
		c: { kind: 'key', glyph: 'c', solid: true },
		A: { kind: 'door', glyph: 'A', solid: true },
		B: { kind: 'door', glyph: 'B', solid: true },
		C: { kind: 'door', glyph: 'C', solid: true }
	};
	// Portal digits read as solid violet tokens: a tint of their own colour leaves the digit
	// too close to its background at thumbnail size.
	for (const digit of ['1', '2', '3', '4', '5', '6', '7', '8', '9'])
		TILE_LOOK[digit] = { kind: 'portal', glyph: digit, solid: true };

	const KINDS = ['rock', 'treat', 'conveyor', 'mirror', 'key', 'door', 'portal'] as const;

	/** Tiles present, in a fixed order, so the aria-label describes what the picture shows. */
	const tileSummary = $derived.by(() => {
		const counts = new Map<string, number>(KINDS.map((k) => [k, 0]));
		for (const row of parsed.cells)
			for (const tile of row) {
				const kind = tile === '#' ? 'rock' : TILE_LOOK[tile]?.kind;
				if (kind) counts.set(kind, (counts.get(kind) ?? 0) + 1);
			}
		if (parsed.treatCount) counts.set('treat', parsed.treatCount);
		return [...counts]
			.filter(([, n]) => n > 0)
			.map(([kind, n]) => `${n} ${kind}${n === 1 ? '' : 's'}`)
			.join(', ');
	});

	const ariaLabel = $derived(
		`${label || `Map preview ${parsed.width} by ${parsed.height}`}${tileSummary ? `. Tiles: ${tileSummary}` : ''}`
	);

	/** Glyphs scale with the cell (container query units) but never past 14px on a tiny map. */
	const glyphSize = $derived(`min(${((0.62 * 100) / parsed.width).toFixed(3)}cqi, 14px)`);

	function cellStyle(tile: string): string {
		const look = TILE_LOOK[tile];
		if (!look) return `font-size: ${glyphSize}`;
		// Keys and doors share one tone per letter, so a thumbnail shows which key belongs to which door.
		if (look.kind === 'key' || look.kind === 'door')
			return `background: ${lockColor(look.glyph)}; color: ${lockInk(look.glyph)}; font-size: ${glyphSize}`;
		const paint = look.solid
			? `background: var(--board-${look.kind}); color: var(--board-${look.kind}-ink)`
			: `background: color-mix(in srgb, var(--board-${look.kind}) 26%, transparent); color: var(--board-${look.kind})`;
		return `${paint}; font-size: ${glyphSize}`;
	}
</script>

<div
	class="grid gap-px bg-indigo-200 p-px rounded-md overflow-hidden {fit ? '' : 'w-full max-w-[200px]'}"
	style="{fit ? `width: ${Math.min(1, parsed.width / Math.max(1, parsed.height)) * 100}%; ` : ''}aspect-ratio: {parsed.width} / {parsed.height}; grid-template-columns: repeat({parsed.width}, minmax(0, 1fr)); grid-template-rows: repeat({parsed.height}, minmax(0, 1fr)); container-type: inline-size"
	role="img"
	aria-label={ariaLabel}
>
	{#each parsed.cells as row, y}
		{#each row as tile, x}
			{@const isStart = parsed.start?.x === x && parsed.start?.y === y}
			<div
				class="flex items-center justify-center overflow-hidden leading-none {tile === '#' || TILE_LOOK[tile]
					? ''
					: isStart
						? 'bg-indigo-600 text-white'
						: tile === '*'
							? 'bg-amber-400'
							: 'bg-indigo-50'}"
				style={tile === '#' ? `background: var(--board-rock); font-size: ${glyphSize}` : cellStyle(tile)}
			>
				{isStart ? arrow : (TILE_LOOK[tile]?.glyph ?? '')}
			</div>
		{/each}
	{/each}
</div>
