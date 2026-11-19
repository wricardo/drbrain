<script lang="ts">
	import { lockColor, lockInk } from '$lib/tiles';

	/** A key tile: filled bow carrying the letter, blade and teeth in the same tone as its door. */
	let {
		x,
		y,
		letter,
		spent = false,
		cell = 40
	}: { x: number; y: number; letter: string; spent?: boolean; cell?: number } = $props();

	const tone = $derived(lockColor(letter));
	const ink = $derived(lockInk(letter));
</script>

<!-- `spent`: this key's door is already open, so the key is told apart at a glance from one still needed. -->
<g
	transform="translate({x * cell}, {y * cell}) scale({cell / 40})"
	data-testid="key"
	data-letter={letter}
	data-spent={spent}
	opacity={spent ? 0.4 : 1}
	aria-hidden="true"
>
	<circle cx="13" cy="13" r="8.8" fill={tone} />
	<text
		x="13"
		y="13.4"
		text-anchor="middle"
		dominant-baseline="central"
		font-size="10.5"
		font-weight="800"
		fill={ink}>{letter}</text
	>
	<path d="M19.2 19.2 L29.8 29.8" fill="none" stroke={tone} stroke-width="4.2" stroke-linecap="round" />
	<path d="M25.2 25.2 L29.4 21" fill="none" stroke={tone} stroke-width="3.2" stroke-linecap="round" />
	<path d="M28.2 28.2 L32.4 24" fill="none" stroke={tone} stroke-width="3.2" stroke-linecap="round" />
</g>
