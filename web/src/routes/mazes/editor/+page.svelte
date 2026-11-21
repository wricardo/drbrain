<script lang="ts">
	import { pageTitle } from '$lib/mazes/brand';
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/stores';
	import { getContextClient } from '@urql/svelte';
	import { unwrap } from '@drbrain/ui/graphql';
	import { getAdminKey, setAdminKey } from '$lib/mazes/admin';
	import Tower from '$lib/mazes/components/Tower.svelte';
	import { describeError, type DescribedError } from '$lib/mazes/errors';
	import { account } from '$lib/account/stores/account';
	import {
		MAX_LEVELS,
		MAX_SIDE,
		MIN_SIDE,
		PAINT_GLYPHS,
		blankCells,
		clampSide,
		formatLevel,
		glyphLabel,
			localIssues,
	paintCell,
		parseLevel,
		resizeLevels,
		type Cells
	} from '$lib/mazes/levels';
	import {
		CREATE_MAP_MUTATION,
		DELETE_MAP_MUTATION,
		ADMIN_MAP_QUERY,
		MAP_QUERY,
		PUBLISH_MAP_MUTATION,
		UPDATE_MAP_MUTATION,
		VALIDATE_MAP_MUTATION
	} from '$lib/mazes/queries';
	import { canEditMap, isDraft, mapStatusLabel } from '$lib/mazes/maps';
	import { DIRECTION_ARROW, portalColor, tileKind } from '$lib/mazes/tower';
	import type { AdminMap, GameMap, MapInput, MapStatus, MapValidationResult } from '$lib/mazes/types';
	import MiniGrid from '$lib/mazes/components/MiniGrid.svelte';
	import Button from '@drbrain/ui/components/Button.svelte';

	const client = getContextClient();

	let id = $state('');
	let name = $state('');
	let description = $state('');
	let difficulty = $state('easy');
	let maxMoves = $state(200);
	let width = $state(8);
	let height = $state(8);
	let levels = $state<Cells[]>([blankCells(8, 8)]);
	let active = $state(0);

	let brush = $state('o');
	let painting = $state(false);
	let existing = $state(false);
	let adminKey = $state(getAdminKey());
	let busy = $state(false);
	let error = $state<DescribedError | null>(null);
	let notice = $state('');
	let validation = $state<MapValidationResult | null>(null);
	/** Lifecycle metadata of the loaded map, kept in step with the server. */
	let status = $state<MapStatus>('PUBLISHED');
	let ownedByMe = $state(false);
	/** The map requested by the URL, used to avoid rendering a blank editor first. */
	const initialMapId = $page.url.searchParams.get('map');
	/** False when the full layout is unavailable, so saving a blank grid is impossible. */
	let layoutLoaded = $state(!initialMapId);
	/** True once the painter changed the map since the last load/save. */
	let dirty = $state(false);
	let loadState = $state<'new' | 'loading' | 'loaded' | 'limited'>(initialMapId ? 'loading' : 'new');
	let requestedDuplicate = $state(false);
	/** Level 0 preview from the public query, shown when the full layout is unavailable. */
	let previewRows = $state<string[]>([]);
	let previewLevels = $state(0);

	const issues = $derived(localIssues(levels));
	const signedIn = $derived($account.me !== null);
	const hasAdminKey = $derived(adminKey.trim() !== '');
	const ownsMap = $derived(existing && ownedByMe);
	const canEditLoaded = $derived(!existing || canEditMap({ ownedByMe }, hasAdminKey));
	const canSave = $derived(signedIn && layoutLoaded && canEditLoaded);
	const canPublish = $derived(existing && isDraft({ status }) && (ownedByMe || hasAdminKey));
	const canDelete = $derived(existing && canEditLoaded);
	const statusLabel = $derived(existing ? mapStatusLabel({ status }) : '');
	const signInNext = $derived($page.url.pathname + $page.url.search);

	onMount(async () => {
		const params = $page.url.searchParams;
		const mapId = params.get('map');
		if (!mapId) {
			layoutLoaded = true;
			loadState = 'new';
			return;
		}
		await openMap(mapId, ['1', 'true', 'yes'].includes((params.get('duplicate') ?? '').toLowerCase()));
	});

	/**
	 * Loads the full layout. The server now returns it to the map's owner (no
	 * key), to an admin, and to nobody else: another player's map is null and
	 * a shipped map without a key is FORBIDDEN. In those cases the public
	 * preview identifies the map and the limited panel offers publish/delete
	 * or an admin-key retry; Save stays disabled.
	 */
	async function openMap(mapId: string, duplicate: boolean) {
		requestedDuplicate = duplicate;
		loadState = 'loading';
		layoutLoaded = false;
		error = null;
		previewRows = [];
		previewLevels = 0;

		let full: AdminMap | null = null;
		let fullError: DescribedError | null = null;
		try {
			const data = await unwrap<{ adminMap: AdminMap | null }>(client.query(ADMIN_MAP_QUERY, { id: mapId }));
			full = data.adminMap;
		} catch (e) {
			fullError = describeError(e);
		}
		if (full) {
			load(full, duplicate);
			layoutLoaded = true;
			loadState = 'loaded';
			return;
		}

		// The full layout is unavailable. The public catalog still exposes the
		// map's metadata and its level-0 preview for published maps and the
		// viewer's own drafts.
		let summary: GameMap | null = null;
		let publicError: DescribedError | null = null;
		try {
			const data = await unwrap<{ map: GameMap | null }>(client.query(MAP_QUERY, { id: mapId }));
			summary = data.map;
		} catch (e) {
			publicError = describeError(e);
		}

		id = mapId;
		if (summary) {
			name = summary.name;
			description = summary.description;
			difficulty = summary.difficulty;
			maxMoves = summary.maxMoves;
			width = summary.width;
			height = summary.height;
			status = summary.status;
			ownedByMe = summary.ownedByMe;
			previewRows = summary.preview;
			previewLevels = summary.levelCount;
			existing = true;
		} else {
			existing = false;
			ownedByMe = false;
			status = 'PUBLISHED';
		}
		layoutLoaded = false;
		dirty = false;
		loadState = 'limited';

		// FORBIDDEN on a shipped map is the expected admin-only path; the panel
		// explains it. A real failure (network/server) belongs in the banner.
		const failure = [fullError, publicError].find(
			(e) => e && e.code !== 'FORBIDDEN' && e.code !== 'NOT_FOUND'
		);
		error = failure ?? null;
	}

	/** Re-attempts the full-layout load (after a key change or a transient failure). */
	function retryLoad() {
		if (id) void openMap(id, requestedDuplicate);
	}

	/** Resets the editor to a blank map without remounting the route. */
	function newMap() {
		id = '';
		name = '';
		description = '';
		difficulty = 'easy';
		maxMoves = 200;
		width = 8;
		height = 8;
		levels = [blankCells(8, 8)];
		active = 0;
		existing = false;
		ownedByMe = false;
		status = 'PUBLISHED';
		layoutLoaded = true;
		dirty = false;
		validation = null;
		error = null;
		notice = '';
		requestedDuplicate = false;
		loadState = 'new';
		void goto('/mazes/editor', { replaceState: true, noScroll: true });
	}

	function load(m: AdminMap, duplicate: boolean) {
		width = m.width;
		height = m.height;
		levels = m.levels.map((rows) => parseLevel(rows, m.width, m.height));
		active = 0;
		id = duplicate ? `${m.id}_copy` : m.id;
		name = duplicate ? `${m.name} (copy)` : m.name;
		description = m.description;
		difficulty = m.difficulty;
		maxMoves = m.maxMoves;
		status = m.status;
		ownedByMe = duplicate ? false : m.ownedByMe;
		existing = !duplicate;
		dirty = false;
		validation = null;
	}

	function invalidate() {
		validation = null;
		notice = '';
		dirty = true;
	}

	function paint(x: number, y: number) {
		invalidate();
		// S and F are unique: painting one elsewhere moves it.
		if (brush === 'S' || brush === 'F') {
			for (const cells of levels) for (const row of cells) for (let i = 0; i < row.length; i++) if (row[i] === brush) row[i] = 'o';
		}
		// Stairs stay paired: painting U/D also places or removes the partner on the adjacent level.
		paintCell(levels, active, x, y, brush);
	}

	function resize(nextWidth: number, nextHeight: number) {
		width = clampSide(nextWidth);
		height = clampSide(nextHeight);
		levels = resizeLevels(levels, width, height);
		invalidate();
	}

	function addLevel() {
		if (levels.length >= MAX_LEVELS) return;
		levels = [...levels, blankCells(width, height)];
		active = levels.length - 1;
		invalidate();
	}

	function removeLevel() {
		if (levels.length <= 1) return;
		if (!confirm(`Remove level ${active + 1}?`)) return;
		levels = levels.filter((_, i) => i !== active);
		active = Math.min(active, levels.length - 1);
		invalidate();
	}

	function clearLevel() {
		if (!confirm(`Clear level ${active + 1}?`)) return;
		levels[active] = blankCells(width, height);
		invalidate();
	}

	function toInput(): MapInput {
		return {
			id: id.trim(),
			name: name.trim(),
			description,
			difficulty,
			levels: levels.map(formatLevel),
			maxMoves: Math.floor(maxMoves)
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
			if (!signedIn) throw new Error('Sign in to validate maps');
			const data = await unwrap<{ validateMap: MapValidationResult }>(client.mutation(VALIDATE_MAP_MUTATION, { map: toInput() }));
			validation = data.validateMap;
		});

	/** Creates or updates the map and adopts the server's full response. */
	async function persist(): Promise<AdminMap> {
		if (!signedIn) throw new Error('Sign in to save maps');
		if (!layoutLoaded) throw new Error('The full layout is unavailable, so saving is disabled');
		let saved: AdminMap;
		if (existing) {
			const data = await unwrap<{ updateMap: AdminMap }>(client.mutation(UPDATE_MAP_MUTATION, { map: toInput() }));
			saved = data.updateMap;
		} else {
			const data = await unwrap<{ createMap: AdminMap }>(client.mutation(CREATE_MAP_MUTATION, { map: toInput() }));
			saved = data.createMap;
		}
		const savedId = saved.id;
		load(saved, false);
		layoutLoaded = true;
		loadState = 'loaded';
		void goto(`/mazes/editor?map=${encodeURIComponent(savedId)}`, { replaceState: true, noScroll: true });
		return saved;
	}

	const save = () =>
		run(async () => {
			const saved = await persist();
			notice = isDraft({ status: saved.status })
				? `Saved "${saved.id}" as a draft. Publish it when the finish is reachable.`
				: `Saved "${saved.id}".`;
		});

	const publish = () =>
		run(async () => {
			if (!signedIn) throw new Error('Sign in to publish maps');
			if (!existing) throw new Error('Save the draft before publishing');
			// Publishing validates the stored map; persist pending painter edits first.
			if (dirty && layoutLoaded) await persist();
			const data = await unwrap<{ publishMap: AdminMap }>(client.mutation(PUBLISH_MAP_MUTATION, { id: id.trim() }));
			load(data.publishMap, false);
			layoutLoaded = true;
			loadState = 'loaded';
			notice = `Published "${data.publishMap.id}". Everyone can play it now.`;
		});

	const remove = () =>
		run(async () => {
			if (!signedIn) throw new Error('Sign in to delete maps');
			if (!confirm(`Delete map "${id}"? Existing sessions keep their own copy.`)) return;
			await unwrap(client.mutation(DELETE_MAP_MUTATION, { id: id.trim() }));
			await goto('/mazes/maps');
		});

	function cellClass(glyph: string): string {
		switch (tileKind(glyph)) {
			case 'void':
				return 'bg-indigo-50 hover:bg-indigo-100';
			case 'start':
				return 'bg-indigo-600 text-white';
			case 'finish':
				return 'bg-amber-400 text-amber-950';
			case 'up':
				return 'bg-slate-300 text-blue-600';
			case 'down':
				return 'bg-slate-300 text-rose-600';
			case 'portal':
				return 'bg-slate-700';
			default:
				return 'bg-slate-300 hover:bg-slate-200';
		}
	}

	const field = 'mt-1 w-full rounded-lg border border-indigo-300 bg-white px-3 py-2 text-sm';
	const btn = 'rounded-lg px-4 py-2 text-sm font-semibold disabled:opacity-50';
</script>

<svelte:head><title>{pageTitle('Map editor')}</title></svelte:head>
<svelte:window onpointerup={() => (painting = false)} />

<div class="mx-auto max-w-6xl px-4 sm:px-6 py-6">
	<div class="flex flex-wrap items-center gap-2">
		<h1 class="text-2xl font-semibold text-indigo-950">Map editor</h1>
		{#if statusLabel}
			<span
				class={`rounded-full px-2 py-0.5 text-xs font-medium ${
					statusLabel === 'Draft' ? 'bg-amber-100 text-amber-900' : 'bg-emerald-50 text-emerald-800'
				}`}
				data-testid="editor-status">{statusLabel}</span
			>
		{/if}
		{#if ownsMap}
			<span class="rounded-full bg-violet-100 px-2 py-0.5 text-xs font-medium text-violet-900" data-testid="editor-yours">Your map</span>
		{/if}
	</div>
	<p class="mt-1 text-sm text-slate-700">
		Pick a glyph, paint each level, validate to see the par, then save a draft. Publishing needs a
		reachable finish. Saving needs a signed-in account; the admin key is only for shipped and legacy maps.
	</p>

	{#if $account.loaded && !signedIn}
		<p class="mt-3 rounded-lg border border-indigo-200 bg-indigo-50 px-3 py-2 text-sm text-indigo-900" data-testid="editor-signin-hint">
			You are not signed in. Sign in to validate, save drafts, publish and delete maps.
			<Button class="ml-2" variant="secondary" href={`/signin?next=${encodeURIComponent(signInNext)}`}>Sign in</Button>
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

	{#if loadState === 'loading'}
		<p class="mt-5 text-sm text-slate-600" role="status">Loading the map…</p>
	{:else if loadState === 'limited'}
		<section class="mt-5 rounded-xl border border-amber-300 bg-amber-50 p-4 text-sm text-amber-950" data-testid="editor-limited">
			<h2 class="text-base font-semibold">{existing ? 'The full layout is unavailable' : 'Map unavailable'}</h2>
			{#if !existing}
				<p class="mt-1">There is no map with this id, or it belongs to another player and is still a draft.</p>
			{:else if ownsMap}
				{#if isDraft({ status })}
					<p class="mt-1">
						Your map’s full layout could not be loaded. Retry below; publishing and deleting do not need it.
					</p>
				{:else}
					<p class="mt-1">Your map’s full layout could not be loaded. Retry below; deleting does not need it.</p>
				{/if}
			{:else if !signedIn}
				<p class="mt-1">
					Sign in to check whether this map is yours. Your own maps open without an admin key; maps that belong
					to another player, and shipped maps, need one.
				</p>
			{:else if hasAdminKey}
				<p class="mt-1">
					This map belongs to another player, or it ships with the server. The admin key did not open it; check
					the key below or retry.
				</p>
			{:else}
				<p class="mt-1">
					This map belongs to another player, or it ships with the server. Enter the admin key to open its full
					layout, or play it from the map gallery.
				</p>
			{/if}
			{#if previewRows.length}
				<div class="mt-3 w-40">
					<MiniGrid rows={previewRows} levelCount={previewLevels} label={`Preview of ${name || id}`} />
					<p class="mt-1 text-xs text-amber-900">
						Level 1 preview{previewLevels > 1 ? ` of ${previewLevels} levels` : ''}. The rest needs the full layout.
					</p>
				</div>
			{/if}
			{#if id}
				<div class="mt-3 flex flex-wrap items-end gap-2">
					{#if existing && !ownsMap}
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
					<button type="button" class="{btn} border border-amber-400 bg-white text-amber-950 hover:bg-amber-100" disabled={busy} onclick={retryLoad}>Retry loading</button>
				</div>
			{/if}
			<div class="mt-3 flex flex-wrap gap-2">
				{#if canPublish}
					<Button onclick={publish} disabled={busy || !signedIn}>{busy ? 'Working…' : 'Publish'}</Button>
				{/if}
				{#if canDelete}
					<button type="button" class="rounded-lg border border-rose-300 px-4 py-2 text-sm font-semibold text-rose-800 hover:bg-rose-50 disabled:opacity-50" disabled={busy || !signedIn} onclick={remove}>
						Delete
					</button>
				{/if}
				<Button variant="secondary" onclick={newMap}>New map</Button>
				<Button variant="quiet" href="/mazes/maps">All maps</Button>
			</div>
		</section>
	{:else}
	<div class="mt-5 grid gap-6 lg:grid-cols-[minmax(0,1.3fr)_minmax(0,1fr)]">
		<section aria-label="Level painter" class="space-y-3">
			<div class="flex flex-wrap items-center gap-2" role="radiogroup" aria-label="Glyph to paint">
				{#each PAINT_GLYPHS as g (g)}
					{@const kind = tileKind(g)}
					<button
						type="button"
						role="radio"
						aria-checked={brush === g}
						aria-label={glyphLabel(g)}
						title="{glyphLabel(g)} ({g})"
						data-testid="brush-{g}"
						class="flex h-9 w-9 items-center justify-center rounded-lg border-2 text-sm font-bold {brush === g ? 'border-indigo-600 ring-2 ring-indigo-300' : 'border-indigo-200'} {cellClass(g)}"
						style={kind === 'portal' ? `color: ${portalColor(g).ring}` : undefined}
						onclick={() => (brush = g)}
					>
						{kind === 'up' ? DIRECTION_ARROW.UP : kind === 'down' ? DIRECTION_ARROW.DOWN : kind === 'void' ? '·' : kind === 'stone' ? '' : g}
					</button>
				{/each}
			</div>

			<div class="flex flex-wrap items-center gap-2" role="tablist" aria-label="Levels">
				{#each levels as _, i (i)}
					<button
						type="button"
						role="tab"
						aria-selected={active === i}
						data-testid="level-tab-{i}"
						class="rounded-lg border-2 px-3 py-1.5 text-sm {active === i ? 'border-indigo-600 bg-indigo-100 font-semibold' : 'border-indigo-200 bg-white'}"
						onclick={() => (active = i)}>Level {i + 1}</button
					>
				{/each}
				<button type="button" class="{btn} border border-indigo-300 text-indigo-900 hover:bg-indigo-50" disabled={levels.length >= MAX_LEVELS} onclick={addLevel} data-testid="add-level">Add level</button>
				<button type="button" class="{btn} border border-indigo-300 text-indigo-900 hover:bg-indigo-50" disabled={levels.length <= 1} onclick={removeLevel} data-testid="remove-level">Remove level</button>
			</div>

			<div class="grid items-start gap-4 sm:grid-cols-[minmax(0,1fr)_auto]">
				<div class="overflow-auto rounded-xl border border-indigo-200 bg-white p-2" role="tabpanel" aria-label="Level {active + 1}">
					<div
						class="inline-grid gap-px bg-indigo-200 p-px select-none touch-none"
						style="grid-template-columns: repeat({width}, 1.75rem)"
						role="grid"
						aria-label="Level {active + 1} cells, {width} by {height}"
					>
						{#each levels[active] as row, y}
							{#each row as glyph, x}
								<button
									type="button"
									class="flex h-7 w-7 items-center justify-center text-xs font-bold {cellClass(glyph)}"
									style={tileKind(glyph) === 'portal' ? `color: ${portalColor(glyph).ring}` : undefined}
									aria-label="Column {x}, row {y}: {glyphLabel(glyph)}"
									data-testid="cell-{x}-{y}"
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
									<span aria-hidden="true"
										>{tileKind(glyph) === 'up' ? DIRECTION_ARROW.UP : tileKind(glyph) === 'down' ? DIRECTION_ARROW.DOWN : tileKind(glyph) === 'void' || tileKind(glyph) === 'stone' ? '' : glyph}</span
									>
								</button>
							{/each}
						{/each}
					</div>
				</div>
				<Tower levelCount={levels.length} current={active} onpeek={(l) => (active = l)} />
			</div>

			<div class="flex flex-wrap items-end gap-3 text-sm">
				<label class="block">Width
					<input type="number" min={MIN_SIDE} max={MAX_SIDE} value={width} onchange={(e) => resize(e.currentTarget.valueAsNumber, height)} class="mt-1 block w-20 rounded-lg border border-indigo-300 px-2 py-1" />
				</label>
				<label class="block">Height
					<input type="number" min={MIN_SIDE} max={MAX_SIDE} value={height} onchange={(e) => resize(width, e.currentTarget.valueAsNumber)} class="mt-1 block w-20 rounded-lg border border-indigo-300 px-2 py-1" />
				</label>
				<button type="button" class="{btn} border border-indigo-300 text-indigo-900" onclick={clearLevel}>Clear level</button>
				<span class="text-xs text-slate-600">glyphs: <code>. o S F U D 1-9</code>; y grows downward; level 1 is the bottom; S must be on level 1 and F on the top level; stairs come in pairs (a U needs a D at the same x,y one level up) — painting one places its partner</span>
			</div>

			{#if issues.length}
				<ul class="text-xs text-amber-800 list-disc pl-5" data-testid="local-issues">
					{#each issues as issue}<li>{issue}</li>{/each}
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
				<label class="block text-sm">Max moves (1–10000)
					<input type="number" min="1" max="10000" class={field} bind:value={maxMoves} oninput={invalidate} />
				</label>
			</div>

			<div class="rounded-xl border border-indigo-200 bg-white p-3 space-y-3">
				<p class="text-xs text-slate-600" data-testid="editor-ownership">
					{#if ownsMap}
						{#if isDraft({ status })}
							Draft: only you can see or play it. Publish it when the finish is reachable.
						{:else}
							Published: everyone can play it. Saving an edit puts it back into draft until you publish again.
						{/if}
					{:else if hasAdminKey}
						Editing as admin. The key is kept for this tab only.
					{:else}
						Shipped and legacy maps need the admin key; your own maps do not.
					{/if}
				</p>
				<label class="block text-sm">Admin key <span class="text-xs text-slate-600">(shipped and legacy maps only)</span>
					<input
						type="password"
						class={field}
						bind:value={adminKey}
						oninput={(e) => setAdminKey(e.currentTarget.value)}
						autocomplete="off"
						placeholder="X-Admin-Key (kept for this tab only)"
					/>
				</label>
				<div class="flex flex-wrap gap-2">
					<button
						type="button"
						class="{btn} border border-indigo-300 text-indigo-900 hover:bg-indigo-50"
						disabled={busy || !signedIn}
						onclick={validate}
						data-testid="validate">Validate</button
					>
					<button
						type="button"
						class="{btn} bg-indigo-600 text-white hover:bg-indigo-700"
						disabled={busy || !canSave || issues.length > 0 || !id.trim() || !name.trim()}
						onclick={save}
						data-testid="save">{existing ? 'Save draft' : 'Create draft'}</button
					>
					{#if canPublish}
						<Button
							onclick={publish}
							disabled={busy || !signedIn || (validation !== null && !validation.finishReachable)}
							data-testid="publish">{busy ? 'Working…' : 'Publish'}</Button
						>
					{/if}
					{#if canDelete}
						<button
							type="button"
							class="{btn} border border-rose-300 text-rose-800 hover:bg-rose-50"
							disabled={busy || !signedIn}
							onclick={remove}
							data-testid="delete">Delete</button
						>
					{/if}
				</div>
				{#if !signedIn}
					<p class="text-xs text-slate-600">Sign in to validate, save, publish or delete.</p>
				{:else if !layoutLoaded}
					<p class="text-xs text-slate-600">The full layout is unavailable, so saving is disabled.</p>
				{:else if existing && !canEditLoaded}
					<p class="text-xs text-slate-600">This map belongs to another player. Only its owner or an admin can change it.</p>
				{/if}
			</div>

			{#if validation}
				<div
					class="rounded-xl border p-3 text-sm {validation.valid && validation.finishReachable
						? 'border-emerald-300 bg-emerald-50 text-emerald-900'
						: validation.valid
							? 'border-amber-300 bg-amber-50 text-amber-950'
							: 'border-rose-300 bg-rose-50 text-rose-900'}"
					role="status"
					data-testid="validation"
				>
					<strong>{validation.valid ? 'Map is structurally valid.' : 'Map has problems:'}</strong>
					{#if validation.issues.length}
						<ul class="mt-1 list-disc pl-5">
							{#each validation.issues as issue}<li>{issue.message}</li>{/each}
						</ul>
					{/if}
					{#if validation.finishReachable}
						<p class="mt-1 font-medium" data-testid="validation-publishable">The finish is reachable, so this map can be published.</p>
					{:else}
						<p class="mt-1 font-medium" data-testid="validation-unreachable">Publishing needs a reachable finish.</p>
					{/if}
					{#if validation.valid || validation.parMoves !== null}
						<p class="mt-1" data-testid="validation-par">
							{validation.finishReachable && validation.parMoves !== null
								? `Par (shortest route): ${validation.parMoves} moves. Fewest level changes: ${validation.minLevelChanges}, forced descents: ${validation.forcedDescents}.`
								: 'No route from the start to the finish yet.'}
						</p>
					{/if}
				</div>
			{/if}
		</section>
	</div>
	{/if}
</div>
