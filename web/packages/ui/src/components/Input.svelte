<script lang="ts">
	import type { HTMLInputAttributes } from 'svelte/elements';
	let { value = $bindable(''), class: className = '', ...attrs }: HTMLInputAttributes = $props();
</script>

<input {...attrs} class="db-input {className}" bind:value />
