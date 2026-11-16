<script lang="ts">
	import { lockColor, lockInk } from '$lib/tiles';

	/**
	 * A door tile.
	 * Locked: a framed leaf in the tone of its key, with a padlock badge.
	 * Open: the frame becomes a recessed doorway, the leaf is swung out to the side and
	 * the letter fades, so "can walk through" is visible in a still frame.
	 */
	let {
		x,
		y,
		letter,
		open = false,
		cell = 40
	}: { x: number; y: number; letter: string; open?: boolean; cell?: number } = $props();

	const tone = $derived(lockColor(letter));
	const ink = $derived(lockInk(letter));
</script>

<g
	transform="translate({x * cell}, {y * cell}) scale({cell / 40})"
	data-testid="door"
	data-letter={letter}
	data-state={open ? 'open' : 'locked'}
	aria-hidden="true"
>
	{#if open}
		<!-- Doorway: a recess, so an open door reads as a way through rather than a panel. -->
		<rect x="4.5" y="4.5" width="25" height="31" rx="3" fill="var(--board-door-recess)" />
		<rect x="4.5" y="4.5" width="25" height="7" rx="3" fill="var(--board-door-recess-lip)" />
		<!-- Leaf swung out on the right jamb, in perspective. -->
		<path d="M24.5 8 L33 12 L33 28 L24.5 32 Z" fill={tone} opacity="0.9" />
		<path d="M24.5 8 L24.5 32" stroke="var(--board-door-frame)" stroke-width="2" fill="none" />
		<text
			x="15.5"
			y="21"
			text-anchor="middle"
			dominant-baseline="central"
			font-size="14"
			font-weight="800"
			fill={tone}>{letter}</text
		>
	{:else}
		<rect
			x="4.5"
			y="4.5"
			width="25"
			height="31"
			rx="3"
			fill={tone}
			stroke="var(--board-door-frame)"
			stroke-width="2.8"
		/>
		<!-- Handle on the leading edge. -->
		<circle cx="24.5" cy="21" r="1.9" fill={ink} opacity="0.55" />
		<text
			x="14.5"
			y="20.5"
			text-anchor="middle"
			dominant-baseline="central"
			font-size="14"
			font-weight="800"
			fill={ink}>{letter}</text
		>
		<!-- Padlock: body plus shackle, in the corner away from the letter. -->
		<rect x="23" y="25.5" width="13" height="10.5" rx="2.2" fill="var(--board-lock-body)" />
		<path
			d="M26 25.5 v-2.4 a3.4 3.4 0 0 1 6.8 0 V25.5"
			fill="none"
			stroke="var(--board-lock-shackle)"
			stroke-width="2"
			stroke-linecap="round"
		/>
	{/if}
</g>
