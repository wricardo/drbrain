<script lang="ts">
	import type { OrderState, OrderView } from '$lib/delivery/api';
	import { deadlineTone } from '$lib/delivery/glyphs';
	import Panel from '$lib/delivery/Panel.svelte';

	interface Props {
		orders: OrderView[];
		selectedCarId: string | null;
		/** Hold size of the selected car, used to explain an unassignable order. */
		selectedCarCapacity?: number | null;
		selectedOrderId?: string | null;
		onAssign?: (orderId: string) => void;
		onUnassign?: (orderId: string) => void;
		onSelectOrder?: (orderId: string) => void;
	}

	let {
		orders,
		selectedCarId,
		selectedCarCapacity = null,
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
		PENDING: { text: 'open', cls: 'bg-track text-body' },
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
	bodyLabel="Orders"
>
	{#if open.length === 0}
		<p class="p-3 text-xs text-faint">
			No open orders. The next customer request arrives on its own schedule.
		</p>
	{/if}

		<ul class="divide-y divide-edge">
			{#each open as o (o.id)}
				{@const tooBigOrder = selectedCarCapacity !== null && o.quantity > selectedCarCapacity}
				<li
					class="border-l-2 transition-colors {o.id === selectedOrderId
						? 'border-[var(--db-accent)] bg-surface-muted'
						: 'border-transparent hover:bg-surface-muted'}"
				>
					<button
						type="button"
						class="w-full px-3 py-2 text-left"
						aria-pressed={o.id === selectedOrderId}
						onclick={() => onSelectOrder?.(o.id)}
					>
						<div class="flex items-baseline justify-between gap-2">
							<span class="text-sm text-body">
								{#if o.quantity > 1}
									<span class="text-[var(--color-warn)]">{o.quantity}×</span>
								{/if}
								{o.itemName}
							</span>
							<span class="rounded px-1.5 py-0.5 text-[11px] {stateBadge[o.state].cls}"
								>{stateBadge[o.state].text}</span
							>
						</div>

						<div class="mt-1 text-xs text-muted">
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

					{#if (o.state === 'PENDING' || o.state === 'ASSIGNED') && (onAssign || onUnassign)}
						<div class="px-3 pb-2">
							{#if o.state === 'PENDING' && onAssign}
								<button
									type="button"
									class="min-h-11 w-full rounded bg-[var(--db-accent)] px-2 py-1.5 text-xs font-semibold text-white transition-colors hover:brightness-110 disabled:cursor-not-allowed disabled:bg-track disabled:text-faint"
									disabled={!selectedCarId || tooBigOrder}
									onclick={() => {
										if (!tooBigOrder) onAssign(o.id);
									}}
									title={!selectedCarId
										? 'Select a car first'
										: tooBigOrder
											? `This order needs ${o.quantity} boxes; the selected car holds ${selectedCarCapacity}`
											: 'Assign to the selected car'}
								>
									{!selectedCarId
										? 'Pick a car first'
										: tooBigOrder
											? `Needs a ${o.quantity}-box vehicle`
											: 'Assign'}
								</button>
								{#if tooBigOrder}
									<p class="mt-1 text-[11px] text-faint">
										The selected car holds {selectedCarCapacity} boxes. Buy a van or truck, or select a
										larger vehicle.
									</p>
								{/if}
							{:else if o.state === 'ASSIGNED' && onUnassign}
								<button
									type="button"
									class="min-h-11 w-full rounded border border-edge-strong px-2 py-1.5 text-xs text-muted hover:text-body"
									onclick={() => onUnassign(o.id)}
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
			<details class="border-t border-edge">
				<summary class="min-h-11 cursor-pointer px-3 py-2 text-xs text-faint hover:text-body">
					{finished.length} completed
				</summary>
				<ul class="divide-y divide-edge">
					{#each finished as o (o.id)}
						<li class="flex items-center justify-between px-3 py-1.5 text-xs">
							<span class="text-faint">
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
