<script lang="ts">
	const games = [
		{ id: 'mazes', name: 'Mazes', blurb: 'Climb the tower. Mind the portals.' },
		{ id: 'motor', name: 'Motor Programming', blurb: 'Program the robot. Collect every treat.' },
		{ id: 'tesla', name: 'Tesla Road Trip', blurb: 'Visit every park before the battery runs out.' },
		{ id: 'delivery', name: 'Delivery', blurb: 'Run the fleet. Grow the city.' }
	];
</script>

<svelte:head><title>Dr Brain</title></svelte:head>

<main class="mx-auto max-w-3xl p-8">
	<h1 class="text-3xl font-semibold">Dr Brain</h1>
	<p class="mt-2 text-gray-600">Four games, one place.</p>
	<ul class="mt-8 grid gap-4 sm:grid-cols-2">
		{#each games as game (game.id)}
			<li>
				<a class="block rounded-lg border p-4 hover:border-indigo-500" href="/{game.id}">
					<span class="font-medium">{game.name}</span>
					<span class="mt-1 block text-sm text-gray-600">{game.blurb}</span>
				</a>
			</li>
		{/each}
	</ul>
</main>
