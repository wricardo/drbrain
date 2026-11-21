export interface GameInfo {
	id: string;
	name: string;
	tag: string;
	description: string;
	art: string;
}

export const games: readonly GameInfo[] = [
	{ id: 'mazes', name: 'Mazes', tag: 'Spatial puzzles', description: 'Find your way up a tower of winding paths and portals.', art: '/art/mazes.webp' },
	{ id: 'motor', name: 'Motor Programming', tag: 'Programming', description: 'Write a tiny program to guide a robot to every treat.', art: '/art/motor.webp' },
	{ id: 'tesla', name: 'Tesla Road Trip', tag: 'Route planning', description: 'Visit every park on a single electric road trip.', art: '/art/tesla.webp' },
	{ id: 'delivery', name: 'Delivery', tag: 'Simulation', description: 'Run a delivery fleet and watch your city grow.', art: '/art/delivery.webp' },
	{ id: 'music', name: 'Music Region', tag: 'Pattern recognition', description: 'Put a scrambled score back together, measure by measure.', art: '/art/music.webp' }
];
