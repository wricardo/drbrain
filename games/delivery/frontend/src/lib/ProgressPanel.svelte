<script lang="ts">
	import type { AchievementView, BuildView, UnlockView } from '$lib/api';

	interface Props {
		achievements: AchievementView[];
		unlocks: UnlockView[];
		builds: BuildView[];
		onBuild?: (id: string) => void;
	}

	let { achievements, unlocks, builds, onBuild }: Props = $props();

	/**
	 * A build is actionable only when it is unlocked, affordable and legally
	 * placeable. The server computes `applicable` and `problem`; the UI must not
	 * second-guess them, or it will offer a button that always fails.
	 */
	const byId = $derived(new Map(builds.map((b) => [b.id, b])));

	function buildFor(unlock: UnlockView): BuildView | undefined {
		const id = unlock.builds[0];
		return id ? byId.get(id) : undefined;
	}

	function canBuild(b: BuildView | undefined): boolean {
		return !!b && b.unlocked && !b.completed && b.affordable && b.applicable;
	}
</script>

<section class="flex min-h-0 flex-col gap-3">
	<div class="rounded-lg border border-ink-600 bg-ink-900">
		<header class="flex items-center justify-between border-b border-ink-700 px-3 py-2">
			<h2 class="text-xs font-semibold tracking-wide text-fog-400 uppercase">City growth</h2>
			<span class="text-xs text-fog-500">
				{unlocks.filter((u) => u.earned).length}/{unlocks.length} unlocked
			</span>
		</header>

		<ul class="divide-y divide-ink-800">
			{#each unlocks as u (u.id)}
				{@const b = buildFor(u)}
				{@const ready = canBuild(b)}
				<li class="px-3 py-2">
					<div class="flex items-baseline justify-between gap-2">
						<span class="text-sm {u.earned ? 'text-fog-300' : 'text-fog-500'}">{u.title}</span>
						{#if b?.completed}
							<span class="rounded bg-[var(--color-money)]/20 px-1.5 py-0.5 text-[11px] text-[var(--color-money)]"
								>built</span
							>
						{:else if u.earned}
							<span class="text-xs text-[var(--color-charger)]">unlocked</span>
						{:else}
							<span class="text-xs text-fog-500">locked</span>
						{/if}
					</div>

					<p class="mt-0.5 text-xs text-fog-500">{u.description}</p>

					{#if b && u.earned && !b.completed}
						<div class="mt-2 flex items-center justify-between gap-2">
							<span class="text-xs {b.affordable ? 'text-[var(--color-money)]' : 'text-fog-500'}">
								${b.cost}
							</span>
							<button
								type="button"
								class="rounded border px-2 py-1 text-xs transition-colors {ready
									? 'border-[var(--color-money)]/50 text-[var(--color-money)] hover:bg-[var(--color-money)]/10'
									: 'cursor-not-allowed border-ink-700 text-fog-500 opacity-60'}"
								disabled={!ready}
								onclick={() => b && onBuild?.(b.id)}
								title={b.problem ?? (b.affordable ? 'Build it' : 'Not enough money yet')}
							>
								{b.affordable ? 'Build' : 'Locked by cost'}
							</button>
						</div>
						{#if b.problem}
							<p class="mt-1 text-[11px] text-[var(--color-warn)]">{b.problem}</p>
						{/if}
					{/if}
				</li>
			{/each}
		</ul>
	</div>

	<div class="rounded-lg border border-ink-600 bg-ink-900">
		<header class="border-b border-ink-700 px-3 py-2">
			<h2 class="text-xs font-semibold tracking-wide text-fog-400 uppercase">Achievements</h2>
		</header>
		<ul class="divide-y divide-ink-800">
			{#each achievements as a (a.id)}
				<li class="px-3 py-1.5">
					<div class="flex items-baseline justify-between gap-2">
						<span class="text-[12px] {a.done ? 'text-[var(--color-money)]' : 'text-fog-400'}">
							{a.done ? '✓' : '·'} {a.title}
						</span>
						{#if !a.done && a.target > 0}
							<span class="text-[11px] text-fog-500">{a.progress}/{a.target}</span>
						{/if}
					</div>
					{#if !a.done && a.target > 0}
						<div class="mt-1 h-1 overflow-hidden rounded-full bg-ink-700">
							<div
								class="h-full rounded-full bg-[var(--color-charger)]"
								style="width: {Math.min(100, (a.progress / a.target) * 100)}%"
							></div>
						</div>
					{/if}
				</li>
			{/each}
		</ul>
	</div>
</section>
