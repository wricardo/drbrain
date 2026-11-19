<script lang="ts">
	import type { CarState, CarView } from '$lib/delivery/api';
	import { batteryTone } from '$lib/delivery/glyphs';
	import Panel from '$lib/delivery/Panel.svelte';

	interface Props {
		cars: CarView[];
		selectedCarId: string | null;
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

	let { cars, selectedCarId, onSelect, onTow, buyable = [], onBuy }: Props = $props();

	// Typed by the generated union: a new car state on the server breaks the
	// build here rather than silently rendering with no colour.
	const stateTone: Record<CarState, string> = {
		IDLE: 'text-fog-400',
		CARRYING: 'text-[var(--color-customer)]',
		CHARGING: 'text-[var(--color-charger)]',
		STRANDED: 'text-[var(--color-danger)]'
	};
</script>

<Panel title="Fleet" summary="{cars.length} vehicle{cars.length === 1 ? '' : 's'}">
	<ul class="divide-y divide-ink-800">
		{#each cars as car, i (car.id)}
			<li>
				<button
					type="button"
					class="flex w-full flex-col gap-1 px-3 py-2 text-left transition-colors hover:bg-ink-800 {car.id ===
					selectedCarId
						? 'bg-ink-800'
						: ''}"
					onclick={() => onSelect?.(car.id)}
					aria-pressed={car.id === selectedCarId}
				>
					<div class="flex items-baseline justify-between gap-2">
						<span class="flex items-baseline gap-2">
							<span class="inline-flex h-4 w-4 items-center justify-center rounded-[2px] bg-fog-400 text-[11px] font-bold text-ink-950"
								>{i + 1}</span
							>
							<span class="text-sm font-medium text-fog-300">{car.name}</span>
							<span class="text-xs text-fog-500">{car.className}</span>
						</span>
						<span class="text-xs font-medium {stateTone[car.state] ?? ''}">{car.state}</span>
					</div>

					<div class="flex items-center gap-2">
						<div class="h-1.5 flex-1 overflow-hidden rounded-full bg-ink-700">
							<div
								class="h-full rounded-full bg-current {batteryTone(car.batteryPct)}"
								style="width: {car.batteryPct}%"
							></div>
						</div>
						<span class="text-xs {batteryTone(car.batteryPct)}"
							>{car.battery}/{car.maxBattery}</span
						>
					</div>

					<div class="flex items-center justify-between text-xs text-fog-500">
						<span>at ({car.x},{car.y})</span>
						<span>
							{#if car.load > 0}
								carrying {car.load}/{car.capacity} boxes
							{:else}
								empty · {car.capacity} box{car.capacity === 1 ? '' : 'es'}
							{/if}
						</span>
					</div>

					{#if car.lastReject}
						<div class="text-xs text-[var(--color-warn)]">
							last move rejected: {car.lastReject}
							{#if car.lastReject === 'OCCUPIED'}
								<span class="text-fog-500">— another car holds that tile; it may move next tick</span>
							{:else if car.lastReject === 'CLOSED'}
								<span class="text-fog-500">— street works; wait it out</span>
							{:else if car.lastReject === 'BLOCKED'}
								<span class="text-fog-500">— water, building or unexplored; drive around</span>
							{:else if car.lastReject === 'NO_BATTERY'}
								<span class="text-fog-500">— charge up</span>
							{/if}
						</div>
					{/if}
				</button>

				{#if car.state === 'STRANDED'}
					<div class="px-3 pb-2">
						<button
							type="button"
							class="w-full rounded border border-[var(--color-danger)]/50 px-2 py-1 text-xs text-[var(--color-danger)] hover:bg-[var(--color-danger)]/10"
							onclick={() => onTow?.(car.id)}
						>
							Tow to depot
						</button>
					</div>
				{/if}
			</li>
		{/each}
	</ul>

	{#if buyable.length > 0}
		<div class="border-t border-ink-700 p-3">
			<h3 class="mb-2 text-[11px] font-semibold tracking-wide text-fog-400 uppercase">Buy a car</h3>
			<ul class="flex flex-col gap-1.5">
				{#each buyable as b (b.cls)}
					<li class="flex items-center justify-between gap-2">
						<span class="text-xs">
							<span class="text-fog-300">{b.cls}</span>
							<span class="ml-1 text-fog-500">
								{b.capacity} slot{b.capacity === 1 ? '' : 's'} · {b.maxBattery}⚡
							</span>
						</span>
						<button
							type="button"
							class="rounded border px-2 py-1 text-xs transition-colors {b.affordable
								? 'border-[var(--color-money)]/50 text-[var(--color-money)] hover:bg-[var(--color-money)]/10'
								: 'cursor-not-allowed border-ink-700 text-fog-500'}"
							disabled={!b.affordable}
							title={b.affordable
								? `Buy a ${b.cls} for $${b.price}`
								: b.unlocked
									? `Costs $${b.price} — not enough money yet`
									: `${b.cls} is not unlocked yet`}
							onclick={() => onBuy?.(b.cls)}
						>
							${b.price}
						</button>
					</li>
				{/each}
			</ul>
		</div>
	{/if}
</Panel>
