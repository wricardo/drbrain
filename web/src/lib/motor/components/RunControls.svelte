<script lang="ts">
	import { SPEED_PRESETS, presetFor } from '$lib/motor/speed';

	let {
		playing,
		finished,
		busy = false,
		canReset,
		empty = false,
		canStepBack = false,
		showBack = true,
		speedMs = $bindable(500),
		onrun,
		onpause,
		onstep,
		onstepback = () => {},
		onspeed = () => {},
		onreset
	}: {
		/** The server is running the program. */
		playing: boolean;
		/** The run ended (won or lost): the primary button offers "Try again". */
		finished: boolean;
		busy?: boolean;
		/** The robot has moved or a run is under way, so Reset has something to undo. */
		canReset: boolean;
		/** The Main tape has no instruction, so running would only waste an attempt. */
		empty?: boolean;
		/** The robot has taken at least one step and the program is saved, so one step can be taken back. */
		canStepBack?: boolean;
		/** False on a spectator view, which has no program to replay and no Back shortcut. */
		showBack?: boolean;
		/** Milliseconds per step; changed with the presets, also while a run is playing. */
		speedMs?: number;
		onrun: () => void;
		onpause: () => void;
		onstep: () => void;
		onstepback?: () => void;
		/** A speed preset was chosen (also while running: the page restarts playback at the new speed). */
		onspeed?: (ms: number) => void;
		onreset: () => void;
	} = $props();

	const preset = $derived(presetFor(speedMs));
	const emptyTitle = 'Put an instruction in Main first';

	// Narrower padding on phones keeps Run/Back/Step/Reset on one row at 360px.
	const base =
		'inline-flex items-center justify-center gap-1.5 rounded-lg px-3 py-2 text-sm font-semibold transition-colors sm:px-4 disabled:cursor-not-allowed disabled:opacity-50';
	const secondary = `${base} border border-indigo-300 text-indigo-900 hover:bg-indigo-50 disabled:hover:bg-transparent`;
</script>

{#snippet icon(path: string)}
	<!-- Labels carry the meaning on phones, where four icon+text buttons do not fit one row. -->
	<svg viewBox="0 0 24 24" class="h-4 w-4 fill-current max-sm:hidden" aria-hidden="true"><path d={path} /></svg>
{/snippet}

<div class="space-y-3" data-testid="run-controls">
	<div class="flex flex-wrap gap-2" role="group" aria-label="Run controls">
		{#if playing}
			<button
				type="button"
				data-testid="pause"
				class="{base} flex-1 bg-amber-500 text-white hover:bg-amber-600"
				disabled={busy}
				title="Pause (Ctrl+Enter)"
				onclick={onpause}
			>
				{@render icon('M6 5h4v14H6zM14 5h4v14h-4z')}Pause
			</button>
		{:else if finished}
			<button
				type="button"
				data-testid="retry"
				class="{base} flex-1 bg-indigo-600 text-white hover:bg-indigo-700"
				disabled={busy}
				title="Put the robot back at the start (R)"
				onclick={onreset}
			>
				{@render icon('M12 5V1L7 6l5 5V7c3.31 0 6 2.69 6 6s-2.69 6-6 6-6-2.69-6-6H4c0 4.42 3.58 8 8 8s8-3.58 8-8-3.58-8-8-8z')}Try again
			</button>
		{:else}
			<button
				type="button"
				data-testid="run"
				class="{base} flex-1 bg-indigo-600 text-white hover:bg-indigo-700"
				disabled={busy || empty}
				title={empty ? emptyTitle : 'Run the program (Ctrl+Enter)'}
				onclick={onrun}
			>
				{@render icon('M8 5v14l11-7z')}Run
			</button>
		{/if}
		{#if showBack}
			<button
				type="button"
				data-testid="stepback"
				class={secondary}
				disabled={busy || playing || !canStepBack}
				title="Go back one step (B)"
				onclick={onstepback}
			>
				{@render icon('M6 5h2v14H6zM9 12l9 7V5z')}Back
			</button>
		{/if}
		<button
			type="button"
			data-testid="step"
			class={secondary}
			disabled={busy || playing || finished || empty}
			title={empty ? emptyTitle : 'Run one step (S)'}
			onclick={onstep}
		>
			{@render icon('M6 5v14l9-7zM17 5h2v14h-2z')}Step
		</button>
		<button
			type="button"
			data-testid="reset"
			class={secondary}
			disabled={busy || !canReset}
			title="Put the robot back at the start (R)"
			onclick={onreset}
		>
			{@render icon('M12 5V1L7 6l5 5V7c3.31 0 6 2.69 6 6s-2.69 6-6 6-6-2.69-6-6H4c0 4.42 3.58 8 8 8s8-3.58 8-8-3.58-8-8-8z')}Reset
		</button>
	</div>

	<div class="flex items-center justify-between gap-3">
		<span id="speed-label" class="text-xs text-slate-700">
			Speed{#if !preset}<span class="ml-1 text-slate-500" data-testid="custom-speed">({speedMs} ms/step)</span>{/if}
		</span>
		<div
			role="radiogroup"
			aria-labelledby="speed-label"
			class="inline-flex overflow-hidden rounded-lg border border-indigo-300"
		>
			{#each SPEED_PRESETS as p (p.id)}
				<button
					type="button"
					role="radio"
					aria-checked={preset?.id === p.id}
					data-testid="speed-{p.id}"
					class="px-3 py-1 text-xs font-semibold transition-colors disabled:cursor-not-allowed disabled:opacity-50 {preset?.id === p.id
						? 'bg-indigo-600 text-white'
						: 'text-indigo-900 hover:bg-indigo-50 disabled:hover:bg-transparent'}"
					disabled={busy}
					onclick={() => {
						// Clicking the active preset is a no-op: while playing it would pause and
						// restart the run for a speed that is already set.
						if (preset?.id === p.id) return;
						speedMs = p.ms;
						onspeed(p.ms);
					}}>{p.label}</button
				>
			{/each}
		</div>
	</div>
</div>
