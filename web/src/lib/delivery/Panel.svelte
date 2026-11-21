<script lang="ts">
	import { untrack, type Snippet } from 'svelte';

	/**
	 * A dashboard panel with a collapsible body.
	 *
	 * Every panel on the play screen uses this, so the affordance is identical
	 * everywhere: the title row is the toggle, the chevron says which way it
	 * will go, and the badge on the right is visible either way so a collapsed
	 * panel still reports its count.
	 *
	 * Collapsed state is remembered per panel in localStorage. The play screen
	 * is a dashboard someone leaves open for a long time; an expanded
	 * "Achievements" list they hid once should stay hidden, rather than
	 * reopening on every reload.
	 */
	interface Props {
		title: string;
		/** Right-aligned badge, e.g. "3 vehicles". Rendered in both states. */
		summary?: string;
		/**
		 * Stable key for the remembered state. Defaults to the title, which is
		 * fine because titles are unique on this screen — pass one explicitly if
		 * that ever stops being true.
		 */
		storeKey?: string;
		defaultOpen?: boolean;
		/** Extra classes on the <section>. */
		class?: string;
		/** Extra classes on the body wrapper, e.g. for an inner scroller. */
		bodyClass?: string;
		/** When set, the body is a labelled, keyboard-focusable scroll region. */
		bodyLabel?: string;
		children: Snippet;
	}

	let {
		title,
		summary = '',
		storeKey = title,
		defaultOpen = true,
		class: className = '',
		bodyClass = '',
		bodyLabel = '',
		children
	}: Props = $props();

	const STORE_KEY = `drbrain:panel:${untrack(() => storeKey).toLowerCase().replace(/\s+/g, '-')}`;

	function restored(): boolean {
		// Guarded because this runs during component init and localStorage is not
		// guaranteed: private-mode Safari can throw on read, and a thrown error
		// here would take down the whole screen rather than one panel.
		try {
			const saved = localStorage.getItem(STORE_KEY);
			return saved === null ? defaultOpen : saved === '1';
		} catch {
			return defaultOpen;
		}
	}

	let open = $state(restored());

	function toggle() {
		open = !open;
		try {
			localStorage.setItem(STORE_KEY, open ? '1' : '0');
		} catch {
			/* remembering is a nicety; never break the toggle over it */
		}
	}

	const bodyId = `panel-${STORE_KEY.replace(/[^a-z0-9-]/g, '')}`;
</script>

<!--
	shrink-0 matters: the play screen's sidebar is a flex column with
	overflow-y-auto, and a flex item shrinks by default. Without this a panel
	whose content is taller than the space left gets compressed and its last row
	is clipped (measured: the Orders body squeezed to 84px against 114px of
	content). Each panel takes its natural height and the sidebar scrolls.
-->
<section class="shrink-0 rounded-lg border border-edge-strong bg-surface {className}">
	<!--
		The whole header row is the control, so the hit target is the full width
		of the panel rather than a small chevron.
	-->
	<h2 class="m-0">
		<button
			type="button"
			class="flex min-h-11 w-full items-center justify-between gap-2 px-3 py-2 text-left transition-colors hover:bg-surface-muted {open
				? 'border-b border-edge'
				: ''}"
			aria-expanded={open}
			aria-controls={bodyId}
			onclick={toggle}
			onkeydown={(e) => {
				// Space and Enter are game keys: pressing them while this button
				// has focus would otherwise queue a tick as well as toggling.
				// Stopping propagation here leaves the button's own activation
				// (which happens on keydown for Enter, keyup for Space) intact.
				if (e.key === ' ' || e.key === 'Enter') e.stopPropagation();
			}}
		>
			<span class="flex items-baseline gap-2">
				<span class="text-xs font-semibold tracking-wide text-muted uppercase">{title}</span>
				{#if summary}
					<span class="text-xs font-normal text-faint">{summary}</span>
				{/if}
			</span>
			<span
				class="text-xs text-faint transition-transform duration-150 {open ? 'rotate-90' : ''}"
				aria-hidden="true">▸</span
			>
		</button>
	</h2>

	{#if open}
		<!-- svelte-ignore a11y_no_noninteractive_tabindex (A labelled scroll region must be keyboard-focusable.) -->
		<div
			id={bodyId}
			class={bodyClass}
			role={bodyLabel ? 'region' : undefined}
			aria-label={bodyLabel || undefined}
			tabindex={bodyLabel ? 0 : undefined}
		>
			{@render children()}
		</div>
	{/if}
</section>
