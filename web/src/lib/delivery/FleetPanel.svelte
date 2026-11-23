<script lang="ts">
	import type { CarState, CarView } from '$lib/delivery/api';
	import { batteryTone } from '$lib/delivery/glyphs';
	import Panel from '$lib/delivery/Panel.svelte';

	interface Props {
		cars: CarView[];
		/** Upkeep charged on the next interval; shown in the panel header. */
		upkeepDue?: number;
		selectedCarId: string | null;
		assignedCarIds: ReadonlySet<string>;
		onSelect?: (id: string) => void;
		onTow?: (id: string) => void;
		buyable?: {
			cls: string;
			price: number;
			affordable: boolean;
			unlocked: boolean;
			capacity: number;
			maxBattery: number;
		}[];
		onBuy?: (cls: string) => void;
	}

	let {
		cars,
		upkeepDue = 0,
		selectedCarId,
		assignedCarIds,
		onSelect,
		onTow,
		buyable = [],
		onBuy
	}: Props = $props();

	// The header is where the player looks before adding a car, and upkeep is
	// the cost of adding one, so the fee lives here rather than in the HUD.
	const heading = $derived(
		`${cars.length} vehicle${cars.length === 1 ? '' : 's'}` +
			(upkeepDue > 0 ? ` · Upkeep Fee $${upkeepDue}` : '')
	);

	// Typed by the generated union: a new car state on the server breaks the
	// build here rather than silently rendering with no colour.
	const stateTone: Record<CarState, string> = {
		IDLE: 'text-muted',
		CARRYING: 'text-[var(--color-customer)]',
		CHARGING: 'text-[var(--color-charger)]',
		STRANDED: 'text-[var(--color-danger)]'
	};
</script>

<Panel
	title="Fleet"
	summary={heading}
	summaryClass={upkeepDue > 0 ? 'text-[var(--color-warn)]' : ''}
>
	<ul class="divide-y divide-edge">
		{#each cars as car, i (car.id)}
			<li>
				<button
					type="button"
					class="flex w-full flex-col gap-1 px-3 py-2 text-left transition-colors hover:bg-surface-muted {car.id ===
					selectedCarId
						? 'bg-surface-muted'
						: ''}"
					onclick={() => onSelect?.(car.id)}
					aria-pressed={car.id === selectedCarId}
				>
					<div class="flex items-baseline justify-between gap-2">
						<span class="flex items-baseline gap-2">
							<span class="relative inline-flex h-4 w-4 items-center justify-center rounded-[2px] bg-ink text-[11px] font-bold text-white {car.cargo.length ? 'drop-shadow-[0_0_4px_#f59e0b]' : assignedCarIds.has(car.id) ? 'drop-shadow-[0_0_4px_#0284c7]' : ''}">
								{i + 1}
								{#if car.cargo.length || assignedCarIds.has(car.id)}<span class="absolute -top-0.5 -left-0.5 h-1.5 w-1.5 rounded-full ring-1 {car.cargo.length ? 'bg-amber-300 ring-amber-900' : 'bg-sky-300 ring-sky-900'}" aria-hidden="true"></span>{/if}
							</span>
							<span class="text-sm font-medium text-body">{car.name}</span>
							<span class="text-xs text-faint">{car.className}</span>
						</span>
						<span class="text-xs font-medium {stateTone[car.state] ?? ''}">{car.state}</span>
					</div>

					<div class="flex items-center gap-2">
						<div class="h-1.5 flex-1 overflow-hidden rounded-full bg-track">
							<div
								class="h-full rounded-full bg-current {batteryTone(car.batteryPct)}"
								style="width: {car.batteryPct}%"
							></div>
						</div>
						<span class="text-xs {batteryTone(car.batteryPct)}"
							>{car.battery}/{car.maxBattery}</span
						>
					</div>

					<div class="flex items-center justify-between text-xs text-faint">
						<span>
							at ({car.x},{car.y})
							{#if car.chargeDue > 0}
								<span class="text-[var(--color-warn)]">· ${car.chargeDue} due</span>
							{/if}
						</span>
						<span>
							{#if car.load > 0}
								carrying {car.load}/{car.capacity} boxes
							{:else}
								{assignedCarIds.has(car.id) ? 'assigned · ' : ''}empty · {car.capacity} box{car.capacity === 1 ? '' : 'es'}
							{/if}
						</span>
					</div>

					{#if car.lastReject}
						<div class="text-xs text-[var(--color-warn)]">
							last move rejected: {car.lastReject}
							{#if car.lastReject === 'OCCUPIED'}
								<span class="text-faint">— another car holds that tile; it may move next tick</span>
							{:else if car.lastReject === 'CLOSED'}
								<span class="text-faint">— street works; wait it out</span>
							{:else if car.lastReject === 'BLOCKED'}
								<span class="text-faint">— water, building or unexplored; drive around</span>
							{:else if car.lastReject === 'NO_BATTERY'}
								<span class="text-faint">— charge up</span>
							{/if}
						</div>
					{/if}
				</button>

				{#if car.state === 'STRANDED' && onTow}
					<div class="px-3 pb-2">
						<button
							type="button"
							class="min-h-11 w-full rounded border border-[var(--color-danger)]/50 px-2 py-1 text-xs text-[var(--color-danger)] hover:bg-[var(--color-danger)]/10"
							onclick={() => onTow(car.id)}
						>
							Tow to depot
						</button>
					</div>
				{/if}
			</li>
		{/each}
	</ul>

	{#if onBuy && buyable.length > 0}
		<div class="border-t border-edge p-3">
			<h3 class="mb-2 text-[11px] font-semibold tracking-wide text-muted uppercase">Buy a car</h3>
			<ul class="flex flex-col gap-1.5">
				{#each buyable as b (b.cls)}
					{@const reason = b.affordable ? '' : b.unlocked ? `Costs $${b.price} — not enough money yet` : `${b.cls} is not unlocked yet`}
					<li>
						<div class="flex items-center justify-between gap-2">
							<span class="text-xs">
								<span class="text-body">{b.cls}</span>
								<span class="ml-1 text-faint">
									{b.capacity} slot{b.capacity === 1 ? '' : 's'} · {b.maxBattery}⚡
								</span>
							</span>
							<button
								type="button"
								class="min-h-11 min-w-11 rounded border px-2 py-1 text-xs transition-colors {b.affordable
									? 'border-[var(--color-money)]/50 text-[var(--color-money)] hover:bg-[var(--color-money)]/10'
									: 'cursor-not-allowed border-edge text-faint'}"
								disabled={!b.affordable}
								aria-describedby={reason ? `buy-reason-${b.cls}` : undefined}
								onclick={() => onBuy(b.cls)}
							>
								${b.price}
							</button>
						</div>
						{#if reason}
							<p id="buy-reason-{b.cls}" class="mt-0.5 text-xs text-muted">{reason}</p>
						{/if}
					</li>
				{/each}
			</ul>
		</div>
	{/if}
</Panel>
