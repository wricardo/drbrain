<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import {
		api,
		ApiError,
		type CityInfo,
		type CityValidationResult,
		type CityView
	} from '$lib/delivery/api';
	import {
		canDeleteCity,
		canEditCity,
		canPublishCity,
		cityConfigFromView,
		formatCityConfigText,
		formatCityJSON,
		parseCityJSON,
		requiresAdminKey,
		starterCityConfig,
		statusLabel
	} from '$lib/delivery/cities';
	import { clearCachedCityConfig, readCachedCityConfig, writeCachedCityConfig } from '$lib/delivery/cityConfigCache';
	import { getAdminKey, setAdminKey } from '$lib/delivery/admin';
	import { pageTitle } from '$lib/delivery/brand';
	import { account } from '$lib/account/stores/account';
	import Button from '@drbrain/ui/components/Button.svelte';
	import Input from '@drbrain/ui/components/Input.svelte';
	import Skeleton from '@drbrain/ui/components/Skeleton.svelte';

	const ID_PATTERN = /^[a-z0-9_-]{1,64}$/;

	let id = $state('');
	let configText = $state(formatCityJSON(starterCityConfig()));
	let meta = $state<CityInfo | null>(null);
	let adminKey = $state(getAdminKey());
	let loading = $state(false);
	let busy = $state<'validate' | 'save' | 'publish' | 'delete' | null>(null);
	let error = $state<string | null>(null);
	let notice = $state('');
	let validation = $state<CityValidationResult | null>(null);
	let deleteArmed = $state(false);

	const signedIn = $derived($account.me !== null);
	const enabled = $derived(signedIn && !loading);
	const existing = $derived(meta !== null);
	const access = $derived(meta ?? { status: 'DRAFT' as const, ownedByMe: true });
	const adminPath = $derived(existing && requiresAdminKey(access));
	const editable = $derived(!existing || canEditCity(access));
	const publishable = $derived(existing && canPublishCity(access));
	const deletable = $derived(existing && canDeleteCity(access));
	const next = $derived(page.url.pathname + page.url.search);
	const signInHref = $derived(`/signin?next=${encodeURIComponent(next)}`);

	let loadedFor: string | null = null;
	let loadSeq = 0;

	function describe(e: unknown): string {
		return e instanceof ApiError ? `${e.message} (${e.code})` : String(e);
	}

	function metaFromView(view: CityView): CityInfo {
		return {
			id: view.id,
			name: view.name,
			gridSize: view.gridSize,
			status: view.status,
			ownedByMe: view.ownedByMe,
			builtin: view.builtin,
			ownerless: view.ownerless
		};
	}

	async function load(cityId: string) {
		const seq = ++loadSeq;
		error = null;
		notice = '';
		validation = null;
		deleteArmed = false;
		if (!cityId) {
			id = '';
			meta = null;
			configText = formatCityJSON(starterCityConfig());
			return;
		}

		loading = true;
		try {
			const view = await api.city(cityId);
			if (seq !== loadSeq) return;
			if (!view) {
				id = cityId;
				meta = null;
				error = `City “${cityId}” was not found, or it is another player’s draft.`;
				return;
			}
			id = view.id;
			meta = metaFromView(view);
			// Prefer the authored document from the server. The rendered view is
			// the fallback only when the config read is unavailable.
			const raw = await api.cityConfig(view.id).catch(() => null);
			if (seq !== loadSeq) return;
			if (raw !== null) {
				configText = formatCityConfigText(raw);
				writeCachedCityConfig(view.id, configText);
			} else {
				const cached = readCachedCityConfig(view.id);
				configText = cached ?? formatCityJSON(cityConfigFromView(view));
				if (!cached) {
					notice =
						'The raw config was unavailable, so the layout and entities were reconstructed from the rendered city. Review items, menus, demands, growth and orders before publishing.';
				}
			}
		} catch (e) {
			if (seq === loadSeq) error = describe(e);
		} finally {
			if (seq === loadSeq) loading = false;
		}
	}

	$effect(() => {
		const queryId = page.url.searchParams.get('id') ?? '';
		if (queryId === loadedFor) return;
		loadedFor = queryId;
		void load(queryId);
	});

	function onAdminKey(value: string) {
		adminKey = value;
		setAdminKey(value);
	}

	function beginNew() {
		loadedFor = '';
		void load('');
		void goto('/delivery/editor', { noScroll: true });
	}

	async function validate() {
		if (!enabled) return;
		const parsed = parseCityJSON(configText);
		if (parsed.error) {
			validation = { valid: false, publishable: false, issues: [parsed.error] };
			return;
		}
		busy = 'validate';
		error = null;
		try {
			validation = await api.validateCity(configText);
		} catch (e) {
			error = describe(e);
		} finally {
			busy = null;
		}
	}

	async function save() {
		if (!enabled) return;
		const wasExisting = existing;
		if (!wasExisting && !ID_PATTERN.test(id)) {
			error = 'Use 1–64 lowercase letters, digits, - or _ for the city id.';
			return;
		}
		const parsed = parseCityJSON(configText);
		if (parsed.error) {
			error = `Config is not valid JSON: ${parsed.error}`;
			return;
		}
		busy = 'save';
		error = null;
		notice = '';
		try {
			const view = wasExisting ? await api.updateCity(id, configText) : await api.createCity(id, configText);
			meta = metaFromView(view);
			id = view.id;
			writeCachedCityConfig(view.id, configText);
			validation = null;
			notice = wasExisting ? 'City updated. Owner edits return it to DRAFT.' : 'Draft saved.';
			if (!wasExisting) {
				loadedFor = view.id;
				history.replaceState(history.state, '', `?id=${encodeURIComponent(view.id)}`);
			}
		} catch (e) {
			error = describe(e);
		} finally {
			busy = null;
		}
	}

	async function publish() {
		if (!enabled || !existing) return;
		busy = 'publish';
		error = null;
		notice = '';
		try {
			const view = await api.publishCity(id);
			meta = metaFromView(view);
			writeCachedCityConfig(view.id, configText);
			notice = 'City published. Anyone can now start a session on it.';
		} catch (e) {
			error = describe(e);
		} finally {
			busy = null;
		}
	}

	async function remove() {
		if (!enabled || !existing) return;
		if (!deleteArmed) {
			deleteArmed = true;
			return;
		}
		busy = 'delete';
		error = null;
		try {
			await api.deleteCity(id);
			clearCachedCityConfig(id);
			await goto('/delivery/cities');
		} catch (e) {
			error = describe(e);
			deleteArmed = false;
		} finally {
			busy = null;
		}
	}
</script>

<svelte:head>
	<title>{pageTitle(existing ? 'Edit city' : 'New city')}</title>
</svelte:head>

<div class="db-container py-8">
	<header class="mb-6 flex flex-wrap items-end justify-between gap-4">
		<div>
			<p class="db-kicker">Delivery</p>
			<h1 class="text-3xl font-bold tracking-tight">{existing ? 'Edit city' : 'New city'}</h1>
			<p class="mt-1 max-w-2xl text-sm text-muted">
				Author the city as JSON. Validate before publishing; drafts may still be unplayable while you work on them.
			</p>
		</div>
		<div class="flex flex-wrap items-center gap-2">
			{#if existing}
				<span class="rounded border border-edge-strong px-2 py-1 text-xs font-semibold text-muted">{statusLabel(access.status)}</span>
			{/if}
			<Button variant="secondary" onclick={beginNew}>New city</Button>
			<Button variant="quiet" href="/delivery/cities">Back to cities</Button>
		</div>
	</header>

	{#if !$account.loaded}
		<Skeleton label="Checking sign-in…" />
	{:else if !signedIn}
		<div class="db-panel p-6">
			<h2 class="text-xl font-bold tracking-tight">Sign in to author cities</h2>
			<p class="mt-2 max-w-xl text-sm text-muted">
				Creating, validating, editing, publishing and deleting a city all need a drbrain account. You can still play published cities as a guest.
			</p>
			<div class="mt-4 flex flex-wrap gap-2">
				<Button href={signInHref}>Sign in</Button>
				<Button variant="secondary" href="/delivery/cities">Browse cities</Button>
			</div>
		</div>
	{:else}
		{#if error}
			<p class="mb-4 rounded-lg border border-[var(--db-danger)]/40 bg-[var(--db-danger)]/10 px-4 py-3 text-sm text-[var(--db-danger)]" role="alert">
				{error}
			</p>
		{/if}
		{#if notice}
			<p class="mb-4 rounded-lg border border-[var(--db-accent)]/35 bg-[var(--db-accent-wash)] px-4 py-3 text-sm text-body">{notice}</p>
		{/if}
		{#if adminPath}
			<div class="db-panel mb-4 p-4">
				<label class="text-sm font-semibold" for="admin-key">Admin key</label>
				<p class="mt-1 text-xs text-muted">Shipped and legacy unowned cities need the Delivery admin key for edit or delete.</p>
				<Input
					id="admin-key"
					class="mt-2 max-w-md"
					type="password"
					autocomplete="off"
					placeholder="X-Admin-Key"
					value={adminKey}
					oninput={(event) => onAdminKey(event.currentTarget.value)}
				/>
			</div>
		{/if}

		{#if loading}
			<Skeleton label="Loading city…" />
		{:else}
			<div class="grid gap-4 lg:grid-cols-[minmax(0,1fr)_20rem]">
				<section class="db-panel min-w-0 p-4" aria-labelledby="config-heading">
					<div class="flex flex-wrap items-center justify-between gap-3">
						<h2 id="config-heading" class="text-lg font-bold tracking-tight">City config</h2>
						<p class="text-xs text-faint">JSON · stored as a city document</p>
					</div>

					<div class="mt-3 grid gap-3 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-end">
						<div>
							<label class="text-sm font-semibold" for="city-id">City id</label>
							<Input
								id="city-id"
								class="mt-1 font-mono"
								value={id}
								disabled={existing || busy !== null}
								autocomplete="off"
								spellcheck="false"
								oninput={(event) => (id = event.currentTarget.value.trim())}
							/>
							<p class="mt-1 text-xs text-muted">
								{existing ? 'The id is permanent once the city exists.' : 'Lowercase letters, digits, - or _; this is the permanent API handle.'}
							</p>
						</div>
						<Button variant="secondary" disabled={!enabled || busy !== null} onclick={validate}>
							{busy === 'validate' ? 'Validating…' : 'Validate'}
						</Button>
					</div>

					<label class="mt-4 block text-sm font-semibold" for="city-config">Config JSON</label>
					<textarea
						id="city-config"
						class="db-input mt-1 min-h-[24rem] w-full resize-y font-mono text-sm leading-6"
						spellcheck="false"
						bind:value={configText}
						disabled={!enabled || busy !== null}
					></textarea>

					{#if validation}
						<div class="mt-4 rounded-lg border border-edge-strong p-3" role="status" aria-live="polite">
							<div class="flex flex-wrap gap-2 text-xs font-semibold">
								<span class="rounded px-2 py-0.5 {validation.valid ? 'bg-[var(--db-success)]/15 text-[var(--db-success)]' : 'bg-[var(--db-danger)]/15 text-[var(--db-danger)]'}">
									{validation.valid ? 'Valid JSON city' : 'Invalid city'}
								</span>
								<span class="rounded px-2 py-0.5 {validation.publishable ? 'bg-[var(--db-success)]/15 text-[var(--db-success)]' : 'bg-surface-muted text-muted'}">
									{validation.publishable ? 'Publishable' : 'Not publishable'}
								</span>
							</div>
							{#if validation.issues.length > 0}
								<ul class="mt-2 list-disc space-y-1 pl-5 text-sm text-body">
									{#each validation.issues as issue, index (index)}
										<li>{issue}</li>
									{/each}
								</ul>
							{/if}
						</div>
					{/if}
				</section>

				<aside class="db-panel flex flex-col gap-3 p-4">
					<div>
						<h2 class="text-lg font-bold tracking-tight">Actions</h2>
						<p class="mt-1 text-xs text-muted">
							{existing ? meta?.name : 'Drafts are visible only to you until published.'}
						</p>
					</div>
					<Button disabled={!enabled || busy !== null || !editable} onclick={save}>
						{busy === 'save' ? 'Saving…' : existing ? 'Update city' : 'Save draft'}
					</Button>
					<Button variant="secondary" disabled={!enabled || busy !== null || !publishable} onclick={publish}>
						{busy === 'publish' ? 'Publishing…' : 'Publish'}
					</Button>
					{#if existing && deletable}
						<div class="mt-2 border-t border-line pt-3">
							{#if deleteArmed}
								<p class="mb-2 text-sm text-[var(--db-danger)]">Delete this city and its saved config? Sessions already started keep their snapshot.</p>
								<div class="flex flex-wrap gap-2">
									<Button variant="secondary" class="border-[var(--db-danger)] text-[var(--db-danger)]" disabled={busy !== null} onclick={remove}>
										{busy === 'delete' ? 'Deleting…' : 'Confirm delete'}
									</Button>
									<Button variant="secondary" disabled={busy !== null} onclick={() => (deleteArmed = false)}>Cancel</Button>
								</div>
							{:else}
								<Button variant="quiet" class="text-[var(--db-danger)]" disabled={busy !== null} onclick={remove}>Delete city</Button>
							{/if}
						</div>
					{/if}
					{#if existing}
						<p class="mt-auto text-xs text-muted">
							Owner edits return a published city to DRAFT. {adminPath ? 'This is the shipped/legacy admin path.' : ''}
						</p>
					{/if}
				</aside>
			</div>
		{/if}
	{/if}
</div>
