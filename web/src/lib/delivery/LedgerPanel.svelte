<script lang="ts">
	import type { CashEntryView, CashKind } from '$lib/delivery/api';
	import Panel from '$lib/delivery/Panel.svelte';

	interface Props {
		entries: CashEntryView[];
		/** Current balance, shown as the panel summary. */
		money: number;
	}

	let { entries, money }: Props = $props();

	// Typed by the generated union: a new cash kind on the server breaks the
	// build here rather than rendering a raw enum value to the player.
	const kindLabel: Record<CashKind, string> = {
		OPENING: 'Opening balance',
		DELIVERY: 'Delivery',
		LATE_DELIVERY: 'Late delivery',
		ORDER_FINE: 'Expired order',
		UPKEEP: 'Fleet upkeep',
		CHARGING: 'Charging',
		TOW: 'Tow',
		CAR_PURCHASE: 'Bought car',
		CAR_SALE: 'Sold car',
		BUILD: 'Build',
		GRANT: 'Grant'
	};

	function signed(amount: number): string {
		return amount > 0 ? `+$${amount}` : `−$${Math.abs(amount)}`;
	}

	function label(kind: CashKind): string {
		return kindLabel[kind] ?? kind;
	}
</script>

<Panel
	title="Checking account"
	summary={`$${money}`}
	bodyClass="max-h-72 overflow-y-auto"
	bodyLabel="Checking account statement"
>
	<ul class="divide-y divide-edge">
		{#each entries as e (e.seq)}
			<li class="flex items-start justify-between gap-3 px-3 py-1.5" data-testid="cash-entry">
				<span class="min-w-0">
					<span class="flex flex-wrap items-baseline gap-x-2 gap-y-1">
						<span class="text-xs text-faint">t{e.tick}</span>
						<span class="text-xs font-medium text-body">{label(e.kind)}</span>
						{#if e.carName}
							<span
								class="rounded bg-[var(--db-accent)]/15 px-1.5 py-0.5 text-[10px] font-semibold text-[var(--db-accent)]"
								title="Charged to {e.carName}"
								>{e.carName}</span
							>
						{/if}
					</span>
					{#if e.memo && e.memo.toLowerCase() !== label(e.kind).toLowerCase()}
						<span class="block truncate text-xs text-faint">{e.memo}</span>
					{/if}
				</span>
				<span class="shrink-0 text-right">
					<span
						class="block text-xs font-semibold tabular-nums {e.amount > 0
							? 'text-[var(--color-money)]'
							: 'text-[var(--color-warn)]'}"
					>
						{signed(e.amount)}
					</span>
					<span class="block text-[11px] tabular-nums text-faint">balance ${e.balance}</span>
				</span>
			</li>
		{/each}
		{#if entries.length === 0}
			<li class="px-3 py-2 text-xs text-faint">
				No money has moved yet. Deliver an order to open the account.
			</li>
		{/if}
	</ul>
</Panel>
