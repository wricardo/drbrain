<script lang="ts">
	import { pageTitle } from '$lib/motor/brand';
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/stores';
	import { getContextClient } from '@urql/svelte';
	import { unwrap } from '@drbrain/ui/graphql';
	import { getAdminKey, setAdminKey } from '$lib/motor/admin';
	import { describeError, type DescribedError } from '@drbrain/ui/errors';
	import {
		CONVEYOR_GLYPHS,
		DOOR_GLYPHS,
		KEY_GLYPHS,
		MAX_SIDE,
		MIN_SIDE,
		MIRROR_GLYPHS,
		PORTAL_GLYPHS,
		START_GLYPHS,
		blankCells,
		copyId,
		formatLayout,
		localLayoutIssues,
		parseLayout,
		resizeCells,
		type StartCell,
		type Tile
	} from '$lib/motor/layout';
	import {
		CREATE_MAP_MUTATION,
		DELETE_MAP_MUTATION,
		MAP_QUERY,
		PUBLISH_MAP_MUTATION,
		UPDATE_MAP_MUTATION,
		VALIDATE_MAP_MUTATION
	} from '$lib/motor/queries';
	import type { Facing, GameMap, MapInput, MapStatus, MapValidationResult } from '$lib/motor/types';
	import { MAX_SUBS } from '$lib/motor/instructions';
	import { account } from '$lib/account/stores/account';
	import Cookie from '@drbrain/ui/components/Cookie.svelte';
	import SignInDialog from '$lib/account/components/SignInDialog.svelte';

	/**
	 * What a click paints: the start cell, plain empty, or any layout glyph. Every
	 * tile glyph is paintable and round-trips through formatLayout unchanged.
	 */
	type Tool = 'start' | 'empty' | Tile;

	const client = getContextClient();

	let id = $state('');
	let name = $state('');
	let description = $state('');
	let difficulty = $state('easy');
	let width = $state(10);
	let height = $state(10);
	let cells = $state<Tile[][]>(blankCells(10, 10));
	let start = $state<StartCell | null>({ x: 0, y: 9, facing: 'RIGHT' });
	let mainTapeLength = $state(10);
	let subTapeLengths = $state<number[]>([10]);
	let maxSteps = $state(200);
	let maxCallDepth = $state(16);
	let allowRecursion = $state(false);

	let tool = $state<Tool>('#');
	let facing = $state<Facing>('RIGHT');
	let painting = $state(false);
	let existing = $state(false);
	/** A shipped map: only an admin may overwrite or delete it. */
	let builtin = $state(false);
	/** The map's publication state; edits always put it back into DRAFT. */
	let status = $state<MapStatus>('DRAFT');
	/** True when the signed-in player created this community map. */
	let owned = $state(false);
	let adminKey = $state(getAdminKey());
	let busy = $state(false);
	let error = $state<DescribedError | null>(null);
	let notice = $state('');
	let validation = $state<MapValidationResult | null>(null);
	/** The sign-in dialog, opened from the hint shown to guests: creating a map needs an account. */
	let signIn = $state(false);
	/** Every map operation needs a signed-in player. */
	const signedIn = $derived($account.me !== null);
	/** Only the owner (or an admin on a shipped map) may change this map. */
	const canEdit = $derived(builtin || owned);
	const canPublish = $derived(existing && !builtin && owned && status === 'DRAFT');
	const next = $derived($page.url.pathname + $page.url.search);

	const layout = $derived(formatLayout(cells, start));
	const parsed = $derived(parseLayout(layout));
	const localIssues = $derived(localLayoutIssues(parsed));
	const unreachable = $derived(new Set((validation?.unreachableTreats ?? []).map((p) => `${p.x},${p.y}`)));

	interface ToolGroup {
		label: string;
		tools: { id: Tool; label: string }[];
	}

	/** Carry direction each conveyor glyph stands for, shown on its paint button. */
	const CONVEYOR_CARRY: Record<string, string> = { U: '↑ up', D: '↓ down', L: '← left', R: '→ right' };

	/**
	 * Grouped so the palette reads as terrain, then the tile effects. Glyphs in
	 * the label always match the character that lands in the layout.
	 */
	const toolGroups: ToolGroup[] = [
		{
			label: 'Terrain',
			tools: [
				{ id: 'empty', label: 'Empty (.)' },
				{ id: '#', label: 'Rock (#)' },
				{ id: '*', label: 'Treat (*)' },
				{ id: 'start', label: 'Start' }
			]
		},
		{
			label: 'Conveyors (carry one cell)',
			tools: CONVEYOR_GLYPHS.map((g) => ({
				id: g as Tool,
				label: `${CONVEYOR_CARRY[g]} ${g}`
			}))
		},
		{
			label: 'Mirrors (turn on arrival)',
			tools: MIRROR_GLYPHS.map((g) => ({ id: g as Tool, label: g === '/' ? 'Slash (/)' : 'Backslash (\\)' }))
		},
		{
			label: 'Keys (open every matching door)',
			tools: KEY_GLYPHS.map((g) => ({ id: g as Tool, label: `Key ${g}` }))
		},
		{
			label: 'Doors (need their key)',
			tools: DOOR_GLYPHS.map((g) => ({ id: g as Tool, label: `Door ${g}` }))
		},
		{
			label: 'Portals (pairs)',
			tools: PORTAL_GLYPHS.map((g) => ({ id: g as Tool, label: g }))
		}
	];

	/** Human name of a cell for its aria-label. */
	const TILE_NAME: Record<string, string> = {
		'.': 'empty',
		'#': 'rock',
		'*': 'treat',
		U: 'conveyor pushing up',
		D: 'conveyor pushing down',
		L: 'conveyor pushing left',
		R: 'conveyor pushing right',
		'/': 'slash mirror',
		'\\': 'backslash mirror',
		a: 'key a',
		b: 'key b',
		c: 'key c',
		A: 'door A',
		B: 'door B',
		C: 'door C',
		'1': 'portal 1',
		'2': 'portal 2',
		'3': 'portal 3',
		'4': 'portal 4',
		'5': 'portal 5',
		'6': 'portal 6',
		'7': 'portal 7',
		'8': 'portal 8',
		'9': 'portal 9'
	};

	/** What a painted cell shows: arrows for conveyors, the glyph itself otherwise. */
	const TILE_GLYPH_DISPLAY: Record<string, string> = { U: '↑', D: '↓', L: '←', R: '→' };

	/** Cell tint per tile in the editor grid; theme-token classes, no raw colours. */
	const TILE_CLASS: Record<string, string> = {
		U: 'bg-sky-100',
		D: 'bg-sky-100',
		L: 'bg-sky-100',
		R: 'bg-sky-100',
		'/': 'bg-slate-200',
		'\\': 'bg-slate-200',
		a: 'bg-amber-100',
		b: 'bg-amber-100',
		c: 'bg-amber-100',
		A: 'bg-amber-300 dark:text-white',
		B: 'bg-amber-300 dark:text-white',
		C: 'bg-amber-300 dark:text-white',
		1: 'bg-violet-100',
		2: 'bg-violet-100',
		3: 'bg-violet-100',
		4: 'bg-violet-100',
		5: 'bg-violet-100',
		6: 'bg-violet-100',
		7: 'bg-violet-100',
		8: 'bg-violet-100',
		9: 'bg-violet-100'
	};

	const tileName = (tile: Tile): string => TILE_NAME[tile] ?? 'empty';
	const tileDisplay = (tile: Tile): string => TILE_GLYPH_DISPLAY[tile] ?? tile;
	const facings: Facing[] = ['UP', 'RIGHT', 'DOWN', 'LEFT'];
	const arrows: Record<Facing, string> = { UP: '▲', RIGHT: '▶', DOWN: '▼', LEFT: '◀' };

	onMount(async () => {
		const params = $page.url.searchParams;
		const mapId = params.get('map');
		if (!mapId) return;
		try {
			const data = await unwrap<{ map: GameMap | null }>(client.query(MAP_QUERY, { id: mapId }));
			if (!data.map) throw new Error(`Map "${mapId}" not found`);
			load(data.map, ['1', 'true', 'yes'].includes((params.get('duplicate') ?? '').toLowerCase()));
		} catch (e) {
			error = describeError(e);
		}
	});

	function load(m: GameMap, duplicate: boolean) {
		const p = parseLayout(m.layout);
		width = p.width;
		height = p.height;
		cells = p.cells;
		start = p.start;
		if (p.start) facing = p.start.facing;
		id = duplicate ? copyId(m.id) : m.id;
		name = duplicate ? `${m.name} (copy)` : m.name;
		description = m.description;
		difficulty = m.difficulty;
		mainTapeLength = m.mainTapeLength;
		subTapeLengths = [...m.subTapeLengths];
		maxSteps = m.maxSteps;
		maxCallDepth = m.maxCallDepth;
		allowRecursion = m.allowRecursion;
		existing = !duplicate;
		// A duplicate is a brand-new map: it is never built-in, so it needs
		// no admin key however the source map is stored.
		builtin = duplicate ? false : m.builtin;
		owned = duplicate ? true : m.ownedByMe;
		status = duplicate ? 'DRAFT' : m.status;
		validation = null;
	}

	function invalidate() {
		validation = null;
		notice = '';
	}

	function paint(x: number, y: number) {
		invalidate();
		if (tool === 'start') {
			cells[y][x] = '.';
			start = { x, y, facing };
			return;
		}
		if (start && start.x === x && start.y === y) start = null;
		cells[y][x] = tool === 'empty' ? '.' : tool;
	}

	function setFacing(f: Facing) {
		facing = f;
		if (start) start = { ...start, facing: f };
		invalidate();
	}

	function resize(nextWidth: number, nextHeight: number) {
		const w = Math.max(MIN_SIDE, Math.min(MAX_SIDE, Math.floor(nextWidth) || MIN_SIDE));
		const h = Math.max(MIN_SIDE, Math.min(MAX_SIDE, Math.floor(nextHeight) || MIN_SIDE));
		width = w;
		height = h;
		cells = resizeCells(cells, w, h);
		if (start && (start.x >= w || start.y >= h)) start = null;
		invalidate();
	}

	function clearAll() {
		if (!confirm('Clear the whole grid?')) return;
		cells = blankCells(width, height);
		start = null;
		invalidate();
	}

	function setSubCount(n: number) {
		const next = subTapeLengths.slice(0, n);
		while (next.length < n) next.push(6);
		subTapeLengths = next;
		invalidate();
	}

	/** Options for the "Subroutines" select: 0..MAX_SUBS. */
	const subCountOptions = Array.from({ length: MAX_SUBS + 1 }, (_, n) => n);

	function toInput(): MapInput {
		return {
			id: id.trim(),
			name: name.trim(),
			description,
			difficulty,
			layout,
			mainTapeLength: Math.floor(mainTapeLength),
			subTapeLengths: subTapeLengths.map((n) => Math.floor(n)),
			maxSteps: Math.floor(maxSteps),
			maxCallDepth: Math.floor(maxCallDepth),
			allowRecursion
		};
	}

	async function run(fn: () => Promise<void>) {
		busy = true;
		error = null;
		notice = '';
		try {
			await fn();
		} catch (e) {
			error = describeError(e);
		} finally {
			busy = false;
		}
	}

	const validate = () =>
		run(async () => {
			const data = await unwrap<{ validateMap: MapValidationResult }>(
				client.mutation(VALIDATE_MAP_MUTATION, { map: toInput() })
			);
			validation = data.validateMap;
		});

	// Saving always leaves the map in DRAFT: publishing is a separate, explicit
	// step that runs the full validation.
	async function persist() {
		let m: GameMap;
		if (existing) {
			const data = await unwrap<{ updateMap: GameMap }>(client.mutation(UPDATE_MAP_MUTATION, { map: toInput() }));
			m = data.updateMap;
		} else {
			const data = await unwrap<{ createMap: GameMap }>(client.mutation(CREATE_MAP_MUTATION, { map: toInput() }));
			m = data.createMap;
		}
		existing = true;
		builtin = false;
		owned = true;
		status = m.status;
	}

	const save = () =>
		run(async () => {
			await persist();
			notice = `Saved "${id.trim()}" as a draft. Publish it when it is ready.`;
		});

	const publish = () =>
		run(async () => {
			await persist();
			const data = await unwrap<{ publishMap: GameMap }>(
				client.mutation(PUBLISH_MAP_MUTATION, { id: id.trim() })
			);
			status = data.publishMap.status;
			validation = null;
			notice = `Published "${id.trim()}". Everyone can play it now.`;
		});

	const remove = () =>
		run(async () => {
			if (!confirm(`Delete map "${id}"? Existing sessions keep their own copy.`)) return;
			await unwrap(client.mutation(DELETE_MAP_MUTATION, { id: id.trim() }));
			await goto('/motor/maps');
		});

	// Turns the loaded built-in map into a new player map: the copy gets its own
	// id, needs no admin key, and the original is left untouched.
	const duplicate = () =>
		run(async () => {
			const copyID = copyId(id.trim());
			if (!confirm(`Create "${copyID}" as your own copy? The original stays unchanged.`)) return;
			const data = await unwrap<{ createMap: GameMap }>(
				client.mutation(CREATE_MAP_MUTATION, {
					map: { ...toInput(), id: copyID, name: `${name.trim()} (copy)` }
				})
			);
			await goto(`/motor/editor?map=${encodeURIComponent(data.createMap.id)}`);
			load(data.createMap, false);
			notice = `Created "${data.createMap.id}" as your draft. Publish it when it is ready.`;
		});

	const field = 'mt-1 w-full rounded-lg border border-indigo-300 bg-white px-3 py-2 text-sm';
	const btn = 'rounded-lg px-4 py-2 text-sm font-semibold disabled:opacity-50';
</script>

<svelte:head><title>{pageTitle('Map editor')}</title></svelte:head>
<svelte:window onpointerup={() => (painting = false)} />

<div class="mx-auto max-w-6xl px-4 sm:px-6 py-6">
	<h1 class="text-2xl font-semibold text-indigo-950">Map editor</h1>
	<p class="mt-1 text-sm text-slate-700">
		Paint the board, set the start cell and tape sizes, then save a draft. A draft is visible only to you and
		can be played until you publish it; publishing runs the full validation. Every map operation needs a
		signed-in account, and changing a built-in map needs the admin key.
	</p>

	{#if $account.loaded && !$account.me}
		<p class="mt-3 rounded-lg border border-indigo-200 bg-indigo-50 px-3 py-2 text-sm text-indigo-900" data-testid="editor-signin-hint">
			You are not signed in. Sign in to validate, save drafts, play them and publish.
			{#if $account.providers.length}
				<button type="button" class="font-semibold underline" onclick={() => (signIn = true)}>Sign in</button>
			{/if}
		</p>
	{/if}

	{#if error}
		<p role="alert" class="mt-3 rounded-lg border border-rose-300 bg-rose-50 px-3 py-2 text-sm text-rose-900" data-testid="editor-error">
			<strong>{error.title}</strong>{#if error.code}<span class="ml-1 font-mono text-xs">[{error.code}]</span>{/if}
			{#if error.message}— {error.message}{/if}
		</p>
	{/if}
	{#if notice}
		<p role="status" class="mt-3 rounded-lg border border-emerald-300 bg-emerald-50 px-3 py-2 text-sm text-emerald-900">{notice}</p>
	{/if}

	<div class="mt-5 grid gap-6 lg:grid-cols-[minmax(0,1.3fr)_minmax(0,1fr)]">
		<section aria-label="Board painter" class="space-y-3">
			<div class="space-y-2">
				{#each toolGroups as group (group.label)}
					<div class="flex flex-wrap items-center gap-x-3 gap-y-1">
						<span class="w-full text-xs font-semibold uppercase tracking-wide text-slate-600 sm:w-44 sm:shrink-0">{group.label}</span>
						<div class="flex flex-wrap items-center gap-2" role="radiogroup" aria-label={group.label}>
							{#each group.tools as t (t.id)}
								<button
									type="button"
									role="radio"
									aria-checked={tool === t.id}
									aria-label={t.label}
									class="rounded-lg border-2 px-3 py-1.5 text-sm {tool === t.id
										? 'border-indigo-600 bg-indigo-100 font-semibold'
										: 'border-indigo-200 bg-white'}"
									onclick={() => (tool = t.id)}>{t.label}</button
								>
							{/each}
						</div>
					</div>
				{/each}
				<div class="flex flex-wrap items-center gap-x-3 gap-y-1 border-t border-indigo-100 pt-2">
					<span class="text-xs text-slate-700" id="facing-label">Start facing</span>
					<div class="flex gap-1" role="radiogroup" aria-labelledby="facing-label">
						{#each facings as f (f)}
							<button
								type="button"
								role="radio"
								aria-checked={facing === f}
								aria-label={f.toLowerCase()}
								class="h-8 w-8 rounded-lg border-2 text-sm {facing === f ? 'border-indigo-600 bg-indigo-100' : 'border-indigo-200 bg-white'}"
								onclick={() => setFacing(f)}>{arrows[f]}</button
							>
						{/each}
					</div>
					<span class="text-xs text-slate-600">Start facing applies to the Start tool and to the current start cell.</span>
				</div>
			</div>

			<div class="rounded-xl border border-indigo-200 bg-white p-2">
				<div
					class="mx-auto grid gap-px bg-indigo-200 p-px select-none touch-none"
					style="grid-template-columns: repeat({width}, minmax(0, 1fr)); max-width: min(100%, {width * 3}rem, calc((100vh - 14rem) * {width / height}))"
					role="grid"
					aria-label="Map cells, {width} by {height}"
				>
					{#each cells as row, y}
						{#each row as tile, x}
							{@const isStart = start?.x === x && start?.y === y}
							{@const bad = unreachable.has(`${x},${y}`)}
							<button
								type="button"
								class="flex aspect-square w-full items-center justify-center text-sm {tile === '#'
									? 'bg-slate-500 text-white'
									: isStart
										? 'bg-indigo-600 text-white'
										: TILE_CLASS[tile] ?? 'bg-indigo-50 hover:bg-indigo-100'} {bad ? 'ring-2 ring-inset ring-rose-600' : ''}"
								aria-label="Column {x}, row {y}: {isStart ? `start facing ${start?.facing.toLowerCase()}` : tileName(tile)}{bad ? ', unreachable treat' : ''}"
								onpointerdown={() => {
									painting = true;
									paint(x, y);
								}}
								onpointerenter={() => painting && paint(x, y)}
								onkeydown={(e) => {
									if (e.key === 'Enter' || e.key === ' ') {
										e.preventDefault();
										paint(x, y);
									}
								}}
							>
								{#if isStart && start}
									<span aria-hidden="true">{arrows[start.facing]}</span>
								{:else if tile === '*'}
									<svg viewBox="-16 -16 32 32" class="h-3/4 w-3/4" aria-hidden="true"><Cookie cx={0} cy={0} /></svg>
								{:else if tile !== '.'}
									<span aria-hidden="true" class="font-semibold">{tileDisplay(tile)}</span>
								{/if}
							</button>
						{/each}
					{/each}
				</div>
			</div>

			<div class="flex flex-wrap items-end gap-3 text-sm">
				<label class="block">Width
					<input type="number" min={MIN_SIDE} max={MAX_SIDE} bind:value={width} onchange={(e) => resize(e.currentTarget.valueAsNumber, height)} class="mt-1 block w-20 rounded-lg border border-indigo-300 px-2 py-1" />
				</label>
				<label class="block">Height
					<input type="number" min={MIN_SIDE} max={MAX_SIDE} bind:value={height} onchange={(e) => resize(width, e.currentTarget.valueAsNumber)} class="mt-1 block w-20 rounded-lg border border-indigo-300 px-2 py-1" />
				</label>
				<button type="button" class="{btn} border border-indigo-300 text-indigo-900" onclick={clearAll}>Clear grid</button>
				<span class="text-xs text-slate-600">
					{parsed.treatCount} treats · glyphs: <code>. # * {Object.values(START_GLYPHS).join(' ')} {CONVEYOR_GLYPHS.join(' ')} {MIRROR_GLYPHS.join(' ')} {KEY_GLYPHS.join(' ')} {DOOR_GLYPHS.join(' ')} {PORTAL_GLYPHS.join(' ')}</code>; y grows downward
				</span>
			</div>

			{#if localIssues.length}
				<ul class="text-xs text-amber-800 list-disc pl-5" data-testid="local-issues">
					{#each localIssues as issue}<li>{issue}</li>{/each}
				</ul>
			{/if}
		</section>

		<section aria-label="Map settings" class="space-y-3">
			<div class="grid grid-cols-2 gap-3">
				<label class="col-span-2 block text-sm">Map id <span class="text-xs text-slate-600">(a-z 0-9 _ -)</span>
					<input class={field} bind:value={id} oninput={invalidate} readonly={existing} placeholder="my_map" />
				</label>
				<label class="col-span-2 block text-sm">Name
					<input class={field} bind:value={name} oninput={invalidate} />
				</label>
				<label class="col-span-2 block text-sm">Description
					<textarea class={field} rows="2" bind:value={description} oninput={invalidate}></textarea>
				</label>
				<label class="block text-sm">Difficulty
					<select class={field} bind:value={difficulty} onchange={invalidate}>
						<option>easy</option><option>medium</option><option>hard</option>
					</select>
				</label>
				<label class="block text-sm">Main tape length (1–32)
					<input type="number" min="1" max="32" class={field} bind:value={mainTapeLength} oninput={invalidate} />
				</label>
				<label class="block text-sm">Subroutines (0–{MAX_SUBS})
					<select class={field} value={subTapeLengths.length} onchange={(e) => setSubCount(Number(e.currentTarget.value))}>
						{#each subCountOptions as n}<option value={n}>{n}</option>{/each}
					</select>
				</label>
				<div class="grid gap-2">
					{#each subTapeLengths as _, i}
						<label class="block text-sm">Sub {i + 1} length (1–16)
							<input type="number" min="1" max="16" class={field} bind:value={subTapeLengths[i]} oninput={invalidate} />
						</label>
					{/each}
				</div>
				<label class="block text-sm">Max steps (≤ 10000)
					<input type="number" min="1" max="10000" class={field} bind:value={maxSteps} oninput={invalidate} />
				</label>
				<label class="block text-sm">Max call depth (≤ 64)
					<input type="number" min="1" max="64" class={field} bind:value={maxCallDepth} oninput={invalidate} />
				</label>
				<label class="col-span-2 flex items-center gap-2 text-sm">
					<input type="checkbox" bind:checked={allowRecursion} onchange={invalidate} class="h-4 w-4 accent-indigo-600" />
					Allow recursion (a sub may call itself or a sub already on the stack)
				</label>
			</div>

			<div class="rounded-xl border border-indigo-200 bg-white p-3 space-y-3">
				{#if existing && !builtin}
					<p
						class="rounded-lg px-3 py-2 text-xs {status === 'DRAFT'
							? 'border border-amber-300 bg-amber-50 text-amber-900'
							: 'border border-emerald-300 bg-emerald-50 text-emerald-900'}"
						data-testid="map-status"
					>
						{status === 'DRAFT'
							? 'Draft: only you can see or play it. Publish it when it is ready.'
							: 'Published: everyone can see and play it. Editing it turns it back into a draft.'}
						{#if !owned}
							<span class="block">Only the player who created this map can edit it; duplicate it below to make your own copy.</span>
						{/if}
					</p>
				{/if}
				{#if builtin}
					<p class="rounded-lg border border-amber-300 bg-amber-50 px-3 py-2 text-xs text-amber-900" data-testid="builtin-warning">
						This is a built-in map. Saving or deleting it needs the admin key, and it ships with the game.
						To make changes, duplicate it below and edit your own copy.
					</p>
					<label class="block text-sm">Admin key
						<input
							type="password"
							class={field}
							bind:value={adminKey}
							oninput={(e) => setAdminKey(e.currentTarget.value)}
							autocomplete="off"
							placeholder="X-Admin-Key (kept for this tab only)"
						/>
					</label>
				{/if}
				<div class="flex flex-wrap gap-2">
					<button type="button" class="{btn} border border-indigo-300 text-indigo-900 hover:bg-indigo-50" disabled={busy || !signedIn} onclick={validate}>Validate</button>
					<button
						type="button"
						class="{btn} bg-indigo-600 text-white hover:bg-indigo-700"
						disabled={busy || !signedIn || localIssues.length > 0 || !id.trim() || !name.trim() || (existing && !canEdit)}
						onclick={save}
						data-testid="save-map">{existing ? 'Save draft' : 'Create draft'}</button
					>
					{#if canPublish}
						<button
							type="button"
							class="{btn} bg-emerald-600 text-white hover:bg-emerald-700"
							disabled={busy || !signedIn || localIssues.length > 0 || !id.trim() || !name.trim()}
							onclick={publish}
							data-testid="publish-map">Publish</button
						>
					{/if}
					{#if existing && canEdit}
						<button type="button" class="{btn} border border-rose-300 text-rose-800 hover:bg-rose-50" disabled={busy} onclick={remove}>Delete</button>
					{/if}
					{#if existing}
						<button
							type="button"
							class="{btn} border border-indigo-300 text-indigo-900 hover:bg-indigo-50"
							disabled={busy || !signedIn}
							onclick={duplicate}
							data-testid="duplicate-map">Duplicate as my own map</button
						>
					{/if}
				</div>
			</div>

			{#if validation}
				<div
					class="rounded-xl border p-3 text-sm {validation.valid ? 'border-emerald-300 bg-emerald-50 text-emerald-900' : 'border-rose-300 bg-rose-50 text-rose-900'}"
					role="status"
					data-testid="validation"
				>
					<strong>{validation.valid ? 'Map is valid.' : 'Map has problems:'}</strong>
					{#if validation.issues.length}
						<ul class="mt-1 list-disc pl-5">
							{#each validation.issues as issue}<li>{issue.message}</li>{/each}
						</ul>
					{/if}
					{#if validation.unreachableTreats.length}
						<p class="mt-1">Unreachable treats (ringed in red): {validation.unreachableTreats.map((p) => `(${p.x},${p.y})`).join(', ')}</p>
					{/if}
				</div>
			{/if}
		</section>
	</div>
</div>

{#if signIn}
	<SignInDialog providers={$account.providers} {next} onclose={() => (signIn = false)} />
{/if}
