<script lang="ts">
	import { GAME, pageTitle } from '$lib/motor/brand';
	import AiPromptCard from '@drbrain/ui/components/AiPromptCard.svelte';
	import Cookie from '@drbrain/ui/components/Cookie.svelte';
	import { instructionMeta } from '$lib/motor/instructions';
	import { buildGenericPrompt } from '$lib/motor/prompt';
	import type { Instruction } from '$lib/motor/types';

	const instructions: { ins: Instruction; text: string }[] = [
		{ ins: 'MOVE_FORWARD', text: 'Move one cell in the direction the robot faces. Walls (the edge of the board) and rocks block the move silently — the step is still spent. Landing on a treat collects it.' },
		{ ins: 'TURN_LEFT', text: 'Rotate 90° counter-clockwise without moving.' },
		{ ins: 'TURN_RIGHT', text: 'Rotate 90° clockwise without moving.' },
		{ ins: 'CALL_SUB_1', text: 'Jump into subroutine 1. When it finishes, execution resumes right after this call. CALL_SUB_2 through CALL_SUB_10 work the same way on maps that provide them.' },
		{ ins: 'EMPTY', text: 'Does nothing and costs nothing: empty slots are skipped instantly.' }
	];

	/** Every layout glyph a map can use, in player language. */
	const tileLegend: [string, string, string][] = [
		['.', 'Floor', 'Plain cell: nothing happens beyond the move itself.'],
		['#', 'Rock', 'Cannot be entered. The board edge is a wall too.'],
		['*', 'Treat', 'Collect it by moving onto it. Collect every treat to win.'],
		['^ > v <', 'Start', 'Where the robot starts, and which way it faces.'],
		['U D L R', 'Conveyor', 'After you move onto it, you are carried one extra cell in that direction — up, down, left or right — in the same step. If that extra cell is a rock, the board edge or a closed door you stay put.'],
		['/', 'Slash mirror', 'Arriving turns you 90°: right becomes up, up becomes right, down becomes left, left becomes down.'],
		['\\', 'Backslash mirror', 'Arriving turns you 90° the other way: right becomes down, down becomes right, up becomes left, left becomes up.'],
		['a b c', 'Key', 'Walk over it to pick it up. It opens every matching door for the rest of the run. A key is not needed to win.'],
		['A B C', 'Door', 'Solid like a rock until its key (the same letter, lower case) has been collected.'],
		['1 … 9', 'Portal', 'Each digit appears on exactly two cells. Arriving on one teleports you to its twin in the same step.']
	];

	const origin = typeof window !== 'undefined' ? window.location.origin : '';
	const genericPrompt = $derived(buildGenericPrompt(origin));

	const toc: [string, string][] = [
		['#game', 'The game'],
		['#instructions', 'Instructions'],
		['#tiles', 'Board tiles'],
		['#rules', 'Rules'],
		['#controls', 'Controls'],
		['#ai', 'How an AI plays'],
		['#connect', 'Let an AI play'],
		['#tips', 'Tips']
	];

	const aiSteps: [string, string][] = [
		['Read the map', 'Ask for the layout, the start cell and facing, the treats, the tape lengths and the limits (steps, call depth, recursion).'],
		['Plan on paper', 'Turn the route into straight runs and turns using coordinates (y grows downward), then look for runs and turn sequences that repeat.'],
		['Save and run', 'setProgram stores the program in the session (it resets the robot), then run plays it on the server or step advances one instruction.'],
		['Read the result', 'Check the status and lossReason. If it lost, see which treats remain, fix the program and try again. The attempts counter and the best step count are kept on the session.']
	];

	const strategies: [string, string][] = [
		['Write the route as coordinates first', 'List each straight run and turn from start to treats before touching the tape. Patterns are much easier to see on paper than in slots.'],
		['Package what repeats', 'A row, a U-turn or a “walk then turn” unit that appears twice belongs in a subroutine.'],
		['Nest subroutines', 'A subroutine may call a different one (just not one already running), so a 3-move sub can feed a 9-move sub, which feeds an 18-move sub.'],
		['Mirror images share a core', 'Left and right versions of a manoeuvre can both call the same inner sub and differ only in the turns around it.'],
		['Walls are free', 'A move into a rock or off the board costs a step but does nothing else, so an over-long run of forward moves is safe when a wall will stop it.'],
		['Let the end of a unit set up the next', 'Design a unit so it leaves the robot facing where the next unit begins, then repeat the unit.'],
		['Use recursion for “until it stops”', 'On recursion maps a sub whose last slot calls itself repeats until a wall, the step limit, the call-depth limit or the win ends it.'],
		['Count your budget', 'Compare slots available against the route length. If the route needs far more actions than the slots could ever execute without nesting, nesting or recursion is the point of the map.']
	];

	const keys: [string, string][] = [
		['F', 'Move forward'],
		['L / R', 'Turn left / right'],
		['1 2 3 4 5 6 7 8 9 0', 'Call subroutine 1 / 2 / 3 / 4 / 5 / 6 / 7 / 8 / 9 / 10'],
		['Backspace', 'Clear the slot'],
		['Arrow keys', 'Move between slots'],
		['Ctrl+Z / Ctrl+Shift+Z', 'Undo / redo a program edit'],
		['Ctrl+Enter', 'Run, or pause while running'],
		['S (no slot focused)', 'Step once'],
		['B (no slot focused)', 'Go back one step'],
		['R (no slot focused)', 'Reset the robot to the start'],
		['?', 'Show the keyboard shortcuts'],
		['Esc', 'Close a menu or dialog']
	];
</script>

<svelte:head><title>{pageTitle('How it works')}</title></svelte:head>

<div class="mx-auto max-w-3xl px-4 sm:px-6 py-8 space-y-10">
	<header>
		<h1 class="text-2xl font-semibold text-indigo-950">How {GAME} works</h1>
		<p class="mt-3 text-slate-800">
			A short guide to the robot, its instructions and the rules — and how to hand a session to an AI.
		</p>
		<nav aria-label="On this page" class="mt-4 flex flex-wrap gap-2 text-sm">
			{#each toc as [href, label]}
				<a {href} class="rounded-full border border-indigo-200 bg-white px-3 py-1 text-indigo-800 hover:bg-indigo-50">{label}</a>
			{/each}
		</nav>
	</header>

	<section id="game" class="scroll-mt-6" aria-labelledby="game-h">
		<h2 id="game-h" class="text-xl font-semibold text-indigo-950">The game</h2>
		<p class="mt-3 text-slate-800">
			You don't drive the robot — you write it a program. Fill the tape slots with instructions, press <strong>Run</strong>, and the server
			executes one instruction per step. The robot wins the moment the last treat
			<svg viewBox="-16 -16 32 32" class="inline h-5 w-5 align-text-bottom" role="img" aria-label="cookie"><Cookie cx={0} cy={0} /></svg>
			is collected. It is a puzzle about patterns: the tapes are short, so you win by noticing what repeats and reusing it with subroutines.
			People build programs with clicks or drag and drop; AI agents write them through an API, and any run can be watched live.
		</p>
		<h3 id="board-h" class="mt-5 font-semibold text-indigo-950">The board</h3>
		<ul class="mt-3 list-disc space-y-1 pl-5 text-slate-800">
			<li>The robot starts on a fixed cell facing a fixed direction (shown by the arrow).</li>
			<li>Rocks (<span class="inline-block h-3 w-3 rounded-sm bg-slate-500 align-middle"></span>) cannot be entered. The board edge is a wall too.</li>
			<li>Treats are collected by moving onto them; the little dots are the trail the robot has walked.</li>
			<li>
				<strong>Coordinates:</strong> <code>x</code> is the column and <code>y</code> is the row. Row 0 is the <em>top</em>, so <strong>y grows downward</strong>.
				Facing up means moving to <code>y − 1</code>; right is <code>x + 1</code>; down is <code>y + 1</code>; left is <code>x − 1</code>.
			</li>
		</ul>
	</section>

	<section id="instructions" class="scroll-mt-6" aria-labelledby="ins-h">
		<h2 id="ins-h" class="text-xl font-semibold text-indigo-950">Instructions</h2>
		<ul class="mt-3 space-y-2">
			{#each instructions as { ins, text } (ins)}
				{@const meta = instructionMeta(ins)}
				<li class="flex items-start gap-3 rounded-lg border border-indigo-100 bg-white p-3">
					<span class="flex h-10 w-10 shrink-0 items-center justify-center rounded-md border-2 font-bold {meta.classes}" aria-hidden="true">{meta.glyph}</span>
					<div>
						<p class="font-semibold">{meta.label}{meta.key ? ` (${meta.key})` : ''}</p>
						<p class="text-sm text-slate-700">{text}</p>
					</div>
				</li>
			{/each}
		</ul>
	</section>

	<section id="tiles" class="scroll-mt-6" aria-labelledby="tiles-h">
		<h2 id="tiles-h" class="text-xl font-semibold text-indigo-950">Board tiles</h2>
		<p class="mt-3 text-slate-800">
			Most cells are plain floor, but maps can add tiles that move the robot for you. At most <strong>one</strong> tile effect happens per step:
			a carry, a mirror turn or a teleport — never two in a row. Treats and keys are collected on the cell you finish the step on.
			These effects are part of the map, not a bug, so a step that carries, turns or teleports the robot still costs only one step.
		</p>
		<dl class="mt-3 grid grid-cols-[max-content_1fr] gap-x-4 gap-y-2 text-sm">
			{#each tileLegend as [glyph, name, text]}
				<dt class="font-mono font-semibold text-indigo-900"><code>{glyph}</code></dt>
				<dd><span class="font-semibold text-indigo-950">{name}.</span> <span class="text-slate-700">{text}</span></dd>
			{/each}
		</dl>
	</section>

	<section id="rules" class="scroll-mt-6" aria-labelledby="rules-h">
		<h2 id="rules-h" class="text-xl font-semibold text-indigo-950">Rules</h2>
		<h3 id="tape-h" class="mt-3 font-semibold text-indigo-950">Tapes, steps and limits</h3>
		<ul class="mt-3 list-disc space-y-1 pl-5 text-slate-800">
			<li>The <strong>main tape</strong> runs left to right. Each map also gives you 0–10 <strong>subroutine tapes</strong>; slot counts differ per map.</li>
			<li>Every executed instruction (including a blocked move or a call) is one <strong>step</strong>. Empty slots are free.</li>
			<li>
				You <strong>win</strong> when no treats remain — even on the very last allowed step. You <strong>lose</strong> if the step limit is hit
				(<code>STEP_LIMIT</code>), the main tape ends with treats left (<code>PROGRAM_ENDED</code>), or calls nest deeper than the map's call-depth limit
				(<code>CALL_DEPTH</code>).
			</li>
			<li>Changing the program resets the robot to the start. While a run is playing the tapes are locked; pause first.</li>
			<li>You can <strong>Step</strong> one instruction at a time, or <strong>Back</strong> one step (key <kbd>B</kbd>) to replay backwards. The slot with the solid indigo ring is the next instruction; a violet ring marks a call that is waiting for its subroutine to return; a yellow inner ring shows what just ran.</li>
		</ul>
		<h3 id="rec-h" class="mt-5 font-semibold text-indigo-950">Subroutines and recursion</h3>
		<div class="mt-3 space-y-3 text-slate-800">
			<p>
				A <code>CALL_SUB_k</code> pushes a frame on the <strong>call stack</strong> and runs sub <em>k</em> from its first slot. When the sub reaches its end,
				its frame is popped and the caller continues. There is no separate call-stack panel: the slot rings on the tapes show which
				tape is running and which calls are waiting.
			</p>
			<p>
				On most maps recursion is <strong>not allowed</strong>: a call to the subroutine that is currently running, or to any subroutine already waiting on the stack, is a
				no-op — nothing happens, but the step is still spent. A sub calling a <em>different</em> sub that isn't on the stack is fine.
			</p>
			<p>
				Maps with a <span class="rounded-full bg-violet-100 px-2 py-0.5 text-xs text-violet-800">recursion</span> badge allow a sub to call itself. That lets a tiny program repeat
				work: for example <code>sub1 = [Forward, Call 1]</code> walks forward until something stops it. Depth is bounded by the call-depth limit and by the step limit — and there is no
				tail-call optimisation, so a call as the very last slot still counts toward depth until the sub finishes.
			</p>
		</div>
	</section>

	<section id="controls" class="scroll-mt-6" aria-labelledby="keys-h">
		<h2 id="keys-h" class="text-xl font-semibold text-indigo-950">Controls</h2>
		<p class="mt-2 text-sm text-slate-800">Click a slot (or press Enter on it) to pick an instruction from a menu. Focus a slot (Tab) to use shortcuts. Typing an instruction key fills the slot and moves to the next one.</p>
		<dl class="mt-3 grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1 text-sm">
			{#each keys as [k, d]}
				<dt><kbd class="rounded border border-indigo-200 bg-white px-1.5">{k}</kbd></dt>
				<dd>{d}</dd>
			{/each}
		</dl>
		<p class="mt-2 text-sm text-slate-800">You can also drag instructions from the palette to a slot, or drag one slot onto another to swap them. Hold Alt/Option to copy instead, hold Shift to reorder within a tape (the slots in between shift over), or drop a slot on Erase to clear it.</p>
	</section>

	<section id="ai" class="scroll-mt-6" aria-labelledby="ai-h">
		<h2 id="ai-h" class="text-xl font-semibold text-indigo-950">How an AI plays</h2>
		<ol class="mt-3 flex flex-col gap-3">
			{#each aiSteps as [title, desc], i}
				<li class="flex items-start gap-4 rounded-xl border border-indigo-100 bg-white p-4">
					<span class="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-indigo-600 text-sm font-medium text-white" aria-hidden="true">{i + 1}</span>
					<div>
						<p class="text-sm font-semibold text-indigo-950">{title}</p>
						<p class="mt-0.5 text-sm leading-relaxed text-slate-700">{desc}</p>
					</div>
				</li>
			{/each}
		</ol>
	</section>

	<section id="connect" class="scroll-mt-6" aria-labelledby="connect-h">
		<h2 id="connect-h" class="text-xl font-semibold text-indigo-950">Let an AI play</h2>
		<p class="mt-3 text-sm leading-relaxed text-slate-800">
			Everything an agent needs is in one page: <a href="/motor/llms.txt" target="_blank" rel="noreferrer" class="font-medium underline">llms.txt</a>
			(rules, connection details, and working GraphQL examples). To hand a <em>specific</em> session to an AI, open it and use the
			<strong>“Play with an AI”</strong> card — the copied prompt includes the session id, the map layout, the tape lengths and the exact calls to make.
			To let an agent pick a map and create its own session, copy this prompt:
		</p>
		<div class="mt-3">
			<AiPromptCard
				prompt={genericPrompt}
				blurb="Paste it into an AI chat that can make HTTP requests; it will read llms.txt, create a session and play to win."
			/>
		</div>
		<p class="mt-3 text-sm"><a href="/motor/llms.txt" target="_blank" rel="noreferrer" class="inline-block rounded-full bg-indigo-600 px-5 py-2 font-semibold text-white hover:bg-indigo-700">Open llms.txt</a></p>
	</section>

	<section id="tips" class="scroll-mt-6" aria-labelledby="strat-h">
		<h2 id="strat-h" class="text-xl font-semibold text-indigo-950">Tips</h2>
		<div class="mt-3 grid gap-3 sm:grid-cols-2">
			{#each strategies as [title, desc]}
				<div class="rounded-xl border border-indigo-100 bg-white p-4">
					<p class="text-sm font-semibold text-indigo-950">{title}</p>
					<p class="mt-1 text-sm leading-relaxed text-slate-700">{desc}</p>
				</div>
			{/each}
		</div>
	</section>

	<p><a href="/motor" class="inline-block rounded-lg bg-indigo-600 px-4 py-2 text-sm font-semibold text-white hover:bg-indigo-700">Pick a map</a></p>
</div>
