<script lang="ts">
	import { onMount, type Snippet } from 'svelte';
	let { title, onclose, children }: { title: string; onclose: () => void; children: Snippet } = $props();
	const id = $props.id();
	let dialog: HTMLDialogElement;

	onMount(() => {
		const trigger = document.activeElement;
		if (typeof dialog.showModal === 'function') dialog.showModal();
		else dialog.setAttribute('open', '');
		return () => {
			dialog.close?.();
			if (trigger instanceof HTMLElement && trigger.isConnected) trigger.focus();
		};
	});
</script>

<svelte:window onkeydown={(event) => {
	if (event.key === 'Escape' && typeof dialog?.showModal !== 'function') onclose();
}} />

<dialog
	bind:this={dialog}
	aria-labelledby={id}
	aria-modal="true"
	oncancel={(event) => { event.preventDefault(); onclose(); }}
	onclick={(event) => {
		if (event.target !== dialog) return;
		const rect = dialog.getBoundingClientRect();
		if (event.clientX < rect.left || event.clientX > rect.right || event.clientY < rect.top || event.clientY > rect.bottom) onclose();
	}}
>
	<div class="top">
		<h2 {id}>{title}</h2>
		<button type="button" aria-label="Close" onclick={onclose}>×</button>
	</div>
	{@render children()}
</dialog>

<style>
	dialog { box-sizing: border-box; margin: auto; width: min(460px, calc(100% - 28px)); max-height: calc(100dvh - 28px); padding: clamp(1.25rem, 4vw, 2rem); border: 1px solid var(--db-line); border-radius: var(--db-radius-lg); background: var(--db-surface); color: var(--db-ink); box-shadow: var(--db-shadow-raised); }
	dialog::backdrop { background: #15133188; }
	.top { display: flex; align-items: center; justify-content: space-between; margin-bottom: .75rem; gap: .5rem; }
	h2 { font-size: 1.65rem; line-height: 1.1; font-weight: 700; letter-spacing: -.035em; }
	button { flex: none; border-radius: 10px; font-size: 1.6rem; width: 44px; height: 44px; cursor: pointer; }
	button:hover { background: var(--db-tint); }
</style>
