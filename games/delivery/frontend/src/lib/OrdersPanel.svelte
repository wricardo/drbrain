<script lang="ts">
	import type { OrderState, OrderView } from '$lib/api';
	import { deadlineTone } from '$lib/glyphs';
	import Panel from '$lib/Panel.svelte';

	interface Props {
		orders: OrderView[];
		selectedCarId: string | null;
		selectedOrderId?: string | null;
		onAssign?: (orderId: string) => void;
		onUnassign?: (orderId: string) => void;
		onSelectOrder?: (orderId: string) => void;
	}

	let {
		orders,
		selectedCarId,
		selectedOrderId = null,
		onAssign,
		onUnassign,
		onSelectOrder
	}: Props = $props();

	const open = $derived(orders.filter((o) => o.state !== 'DONE' && o.state !== 'EXPIRED'));
	const finished = $derived(orders.filter((o) => o.state === 'DONE' || o.state === 'EXPIRED'));

	// Typed by the generated union, so adding an order state on the server
	// and regenerating makes this map fail to compile until it is handled.
	const stateBadge: Record<OrderState, { text: string; cls: string }> = {
		PENDING: { text: 'open', cls: 'bg-ink-700 text-fog-300' },
		ASSIGNED: { text: 'ASSIGNED', cls: 'bg-[var(--color-charger)]/20 text-[var(--color-charger)]' },
		PICKED_UP: { text: 'CARRYING', cls: 'bg-[var(--color-customer)]/20 text-[var(--color-customer)]' },
		DONE: { text: 'delivered', cls: 'bg-[var(--color-money)]/20 text-[var(--color-money)]' },
		EXPIRED: { text: 'EXPIRED', cls: 'bg-[var(--color-danger)]/20 text-[var(--color-danger)]' }
	};
</script>

<Panel
	title="Orders"
	summary="{open.length} open"
	bodyClass="max-h-72 overflow-y-auto"
>
	{#if open.length === 0}
		<p class="p-3 text-xs text-fog-500">
			No open orders. The next customer request arrives on its own schedule.
		</p>
	{/if}

		<ul class="divide-y divide-ink-800">
			{#each open as o (o.id)}
				<li
					class="border-l-2 transition-colors {o.id === selectedOrderId
						? 'border-[var(--color-charger)] bg-ink-800'
						: 'border-transparent hover:bg-ink-800'}"
				>
					<button
						type="button"
						class="w-full px-3 py-2 text-left"
						aria-pressed={o.id === selectedOrderId}
						onclick={() => onSelectOrder?.(o.id)}
					>
						<div class="flex items-baseline justify-between gap-2">
							<span class="text-sm text-fog-300">
								{#if o.quantity > 1}
									<span class="text-[var(--color-warn)]">{o.quantity}×</span>
								{/if}
								{o.itemName}
							</span>
							<span class="rounded px-1.5 py-0.5 text-[11px] {stateBadge[o.state].cls}"
								>{stateBadge[o.state].text}</span
							>
						</div>

						<div class="mt-1 text-xs text-fog-400">
							<span class="text-[var(--color-kitchen)]">{o.kitchenName}</span>
							<span class="mx-1">→</span>
							<span class="text-[var(--color-customer)]">{o.customerName}</span>
						</div>

						<div class="mt-1 flex items-center justify-between text-xs">
							<span class="text-[var(--color-money)]">${o.reward}</span>
							<span class={deadlineTone(o.ticksLeft)}>
								{#if o.ticksLeft >= 0}
									{o.ticksLeft} ticks left
								{:else}
									overdue
								{/if}
							</span>
						</div>
					</button>

					{#if o.state === 'PENDING' || o.state === 'ASSIGNED'}
						<div class="px-3 pb-2">
							{#if o.state === 'PENDING'}
								<button
									type="button"
									class="w-full rounded bg-[var(--color-charger)] px-2 py-1.5 text-xs font-semibold text-ink-950 transition-colors hover:brightness-110 disabled:cursor-not-allowed disabled:bg-ink-700 disabled:text-fog-500"
									disabled={!selectedCarId}
									onclick={() => onAssign?.(o.id)}
									title={selectedCarId ? 'Assign to the selected car' : 'Select a car first'}
								>
									{selectedCarId ? 'Assign' : 'Pick a car first'}
								</button>
							{:else}
								<button
									type="button"
									class="w-full rounded border border-ink-600 px-2 py-1.5 text-xs text-fog-400 hover:text-fog-300"
									onclick={() => onUnassign?.(o.id)}
								>
									Unassign
								</button>
							{/if}
						</div>
					{/if}
				</li>
			{/each}
		</ul>

		{#if finished.length > 0}
			<details class="border-t border-ink-800">
				<summary class="cursor-pointer px-3 py-2 text-xs text-fog-500 hover:text-fog-300">
					{finished.length} completed
				</summary>
				<ul class="divide-y divide-ink-800">
					{#each finished as o (o.id)}
						<li class="flex items-center justify-between px-3 py-1.5 text-xs">
							<span class="text-fog-500">
								{#if o.quantity > 1}{o.quantity}× {/if}{o.itemName} → {o.customerName}
							</span>
							<span class={stateBadge[o.state].cls + ' rounded px-1.5 py-0.5 text-[11px]'}>
								{o.state === 'DONE' ? `+$${o.paid ?? o.reward}` : 'EXPIRED'}
							</span>
						</li>
					{/each}
				</ul>
			</details>
		{/if}
</Panel>
