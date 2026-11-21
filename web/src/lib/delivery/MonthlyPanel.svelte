<script lang="ts">
	import type { CashCategory, MonthCategoryView, MonthView } from '$lib/delivery/api';
	import Panel from '$lib/delivery/Panel.svelte';

	interface Props {
		months: MonthView[];
		ticksPerMonth: number;
	}

	let { months, ticksPerMonth }: Props = $props();

	// Typed by the generated enum: a new category on the server breaks the
	// build here rather than rendering a raw enum value to the player.
	const categoryLabel: Record<CashCategory, string> = {
		OPENING: 'Opening balance',
		DELIVERY_INCOME: 'Delivery income',
		REWARDS: 'Rewards',
		ORDER_FINES: 'Expired-order fines',
		UPKEEP: 'Fleet upkeep',
		CHARGING: 'Charging',
		TOWS: 'Tows',
		CAR_PURCHASES: 'Car purchases',
		BUILDS: 'City builds',
		OTHER: 'Other'
	};

	function signed(amount: number): string {
		if (amount > 0) return `+$${amount}`;
		if (amount < 0) return `−$${Math.abs(amount)}`;
		return '$0';
	}

	function categoryAmount(c: MonthCategoryView): string {
		if (c.income > 0 && c.expenses > 0) {
			return `+$${c.income} · −$${c.expenses}`;
		}
		if (c.income > 0) return `+$${c.income}`;
		if (c.expenses > 0) return `−$${c.expenses}`;
		return '$0';
	}

	function categoryTone(c: MonthCategoryView): string {
		return c.income > 0 ? 'text-[var(--color-money)]' : 'text-[var(--color-warn)]';
	}
</script>

<Panel
	title="Monthly statement"
	summary="{months.length} month{months.length === 1 ? '' : 's'} · {ticksPerMonth} ticks each"
	bodyClass="max-h-96 overflow-y-auto"
	bodyLabel="Monthly income and expenses"
>
	<ul class="divide-y divide-edge">
		{#each months as m (m.index)}
			<li class="px-3 py-2" data-testid="month-row">
				<div class="flex items-baseline justify-between gap-2">
					<span class="min-w-0">
						<span class="text-xs font-semibold text-body">Month {m.index}</span>
						<span class="ml-2 text-[11px] tabular-nums text-faint">t{m.startTick}–{m.endTick}</span>
					</span>
					<span
						class="shrink-0 text-xs font-semibold tabular-nums {m.net >= 0
							? 'text-[var(--color-money)]'
							: 'text-[var(--color-warn)]'}"
						data-testid="month-net"
					>
						{signed(m.net)}
					</span>
				</div>
				<div class="mt-0.5 flex flex-wrap gap-x-3 text-[11px] tabular-nums text-faint">
					<span class="text-[var(--color-money)]">+${m.income} in</span>
					<span class="text-[var(--color-warn)]">−${m.expenses} out</span>
				</div>
				{#if m.categories.length > 0}
					<ul class="mt-1 flex flex-col gap-0.5">
						{#each m.categories as c (c.category)}
							<li class="flex items-baseline justify-between gap-2 text-[11px]">
								<span class="min-w-0 truncate text-muted">{categoryLabel[c.category] ?? c.category}</span>
								<span class="shrink-0 tabular-nums {categoryTone(c)}">{categoryAmount(c)}</span>
							</li>
						{/each}
					</ul>
				{:else}
					<p class="mt-1 text-[11px] text-faint">No money moved this month.</p>
				{/if}
			</li>
		{/each}
		{#if months.length === 0}
			<li class="px-3 py-2 text-xs text-faint">No months yet. Deliver an order to open the books.</li>
		{/if}
	</ul>
</Panel>
