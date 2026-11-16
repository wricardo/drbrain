<script lang="ts">
	import { untrack } from 'svelte';
	import type { Facing, GameMap, StepEvent, VMState } from '$lib/types';
	import { nextAngle } from '$lib/grid';
	import { isUnlocked, lockPairText, lockPairs } from '$lib/tiles';
	import Cookie from './Cookie.svelte';
	import DoorGlyph from './DoorGlyph.svelte';
	import KeyGlyph from './KeyGlyph.svelte';

	let {
		map,
		vm = null,
		lastEvent = null,
		compact = false
	}: { map: GameMap; vm?: VMState | null; lastEvent?: StepEvent | null; compact?: boolean } = $props();

	const CELL = 40;

	const pos = $derived(vm?.pos ?? map.start);
	const facing = $derived(vm?.facing ?? map.startFacing);
	const treats = $derived(vm?.treatsRemaining ?? map.treats);
	const trail = $derived(vm?.visited ?? []);
	const blocked = $derived(!!vm && !!lastEvent && lastEvent.blocked && lastEvent.step === vm.steps);

	let angle = $state(0);
	$effect.pre(() => {
		const f = facing;
		angle = nextAngle(untrack(() => angle), f);
	});

	const cx = $derived(pos.x * CELL + CELL / 2);
	const cy = $derived(pos.y * CELL + CELL / 2);
	// Named tiles present, so a screen reader hears about a push or a portal before it moves the robot.
	const tileSummary = $derived(
		(
			[
				[map.conveyors.length, 'conveyor'],
				[map.mirrors.length, 'mirror'],
				[map.keys.length, 'key'],
				[map.doors.length, 'door'],
				[map.portals.length, 'portal']
			] as [number, string][]
		)
			.filter(([n]) => n > 0)
			.map(([n, name]) => `${n} ${name}${n === 1 ? '' : 's'}`)
			.join(', ')
	);
	/** Doors this run has opened, as their letters: those doors are drawn open. */
	const openDoors = $derived(vm?.openDoors ?? []);
	/** Key/door pairings, so the label says which key opens which door and which are open. */
	const locks = $derived(lockPairs(map.keys, map.doors, openDoors));
	const lockLabel = $derived(
		locks.length === 0
			? ''
			: ` Keys: ${lockPairText(locks)}${locks.some((l) => l.open) ? `; open: ${locks.filter((l) => l.open).map((l) => l.door).join(', ')}` : ''}.`
	);
	const label = $derived(
		`Board ${map.width} by ${map.height}. Robot at column ${pos.x}, row ${pos.y} facing ${facing.toLowerCase()}. ${treats.length} treats left.${tileSummary ? ` Tiles: ${tileSummary}.` : ''}${lockLabel}`
	);

	/** Rotation (degrees, clockwise) that points a conveyor chevron in its carry direction. */
	const CONVEYOR_ANGLE: Record<Facing, number> = { UP: 0, RIGHT: 90, DOWN: 180, LEFT: 270 };
</script>

<svg
	viewBox="0 0 {map.width * CELL} {map.height * CELL}"
	class="mx-auto block w-full h-auto rounded-xl border border-indigo-200 bg-indigo-50 shadow-sm"
	style="max-width: {compact
		? `min(100%, calc(16rem * ${map.width / map.height}))`
		: `max(280px, min(${map.width * 72}px, calc((100vh - 13rem) * ${map.width / map.height})))`}"
	role="img"
	aria-label={label}
	data-testid="board"
>
	{#each { length: map.height } as _, y}
		{#each { length: map.width } as __, x}
			<rect
				x={x * CELL}
				y={y * CELL}
				width={CELL}
				height={CELL}
				fill={(x + y) % 2 === 0 ? 'var(--board-tile-a)' : 'var(--board-tile-b)'}
			/>
		{/each}
	{/each}

	{#each trail as p, i (i)}
		<circle cx={p.x * CELL + CELL / 2} cy={p.y * CELL + CELL / 2} r="5" fill="var(--board-trail)" opacity="var(--board-trail-opacity)" />
	{/each}

	{#each map.conveyors as c (`c${c.pos.x},${c.pos.y}`)}
		<g aria-hidden="true">
			<rect x={c.pos.x * CELL + 4} y={c.pos.y * CELL + 4} width={CELL - 8} height={CELL - 8} rx="6" fill="var(--board-conveyor)" opacity="0.35" />
			<g transform="translate({c.pos.x * CELL + CELL / 2}, {c.pos.y * CELL + CELL / 2}) rotate({CONVEYOR_ANGLE[c.dir]})">
				<polygon points="0,-10 9,4 -9,4" fill="var(--board-conveyor)" />
			</g>
		</g>
	{/each}

	{#each map.mirrors as m (`m${m.pos.x},${m.pos.y}`)}
		<line
			x1={m.pos.x * CELL + (m.slash ? 5 : CELL - 5)}
			y1={m.pos.y * CELL + CELL - 5}
			x2={m.pos.x * CELL + (m.slash ? CELL - 5 : 5)}
			y2={m.pos.y * CELL + 5}
			stroke="var(--board-mirror)"
			stroke-width="4"
			stroke-linecap="round"
		/>
	{/each}

	{#each map.keys as k (`k${k.pos.x},${k.pos.y}`)}
		<KeyGlyph x={k.pos.x} y={k.pos.y} letter={k.letter} spent={isUnlocked(openDoors, k.letter)} cell={CELL} />
	{/each}

	{#each map.doors as d (`d${d.pos.x},${d.pos.y}`)}
		<DoorGlyph x={d.pos.x} y={d.pos.y} letter={d.letter} open={isUnlocked(openDoors, d.letter)} cell={CELL} />
	{/each}

	{#each map.portals as p (`p${p.pos.x},${p.pos.y}`)}
		<g aria-hidden="true">
			<circle cx={p.pos.x * CELL + CELL / 2} cy={p.pos.y * CELL + CELL / 2} r="13" fill="none" stroke="var(--board-portal)" stroke-width="3" />
			<!-- The pair digit stays inside the cell: drawn above it, a top-row digit is clipped by the viewBox. -->
			<text
				x={p.pos.x * CELL + CELL / 2}
				y={p.pos.y * CELL + CELL / 2 + 4}
				text-anchor="middle"
				font-size="12"
				font-weight="700"
				fill="var(--board-portal)">{p.pair}</text
			>
		</g>
	{/each}

	{#each map.rocks as r (`${r.x},${r.y}`)}
		<g aria-hidden="true">
			<rect x={r.x * CELL + 3} y={r.y * CELL + 3} width={CELL - 6} height={CELL - 6} rx="8" fill="var(--board-rock)" />
			<rect x={r.x * CELL + 7} y={r.y * CELL + 7} width={CELL - 22} height="8" rx="4" fill="var(--board-rock-shine)" opacity="0.7" />
		</g>
	{/each}

	{#each treats as t (`${t.x},${t.y}`)}
		<Cookie cx={t.x * CELL + CELL / 2} cy={t.y * CELL + CELL / 2} />
	{/each}

	{#if blocked}
		<rect
			x={pos.x * CELL + 2}
			y={pos.y * CELL + 2}
			width={CELL - 4}
			height={CELL - 4}
			rx="8"
			fill="none"
			stroke="var(--board-bump)"
			stroke-width="3"
		/>
	{/if}

	<g class="robot" style="transform: translate({cx}px, {cy}px) rotate({angle}deg)" aria-hidden="true">
		<polygon points="0,-15 12,12 0,6 -12,12" fill="var(--board-robot)" stroke="var(--board-robot-outline)" stroke-width="2" stroke-linejoin="round" />
	</g>
</svg>

<style>
	.robot {
		transition: transform 120ms ease-out;
	}
	@media (prefers-reduced-motion: reduce) {
		.robot {
			transition: none;
		}
	}
</style>
