import { sveltekit } from '@sveltejs/kit/vite';
import tailwindcss from '@tailwindcss/vite';
import { defineConfig, type UserConfig } from 'vite';

// The `test` block is read by vitest at runtime. vitest ships its own vite typings
// (a different major than the app's vite), so the config is asserted as a plain vite UserConfig.
export default defineConfig({
	plugins: [tailwindcss(), sveltekit()],
	resolve: {
		conditions: ['browser']
	},
	server: {
		// One backend for all four game prefixes; override for a deployed host.
		proxy: Object.fromEntries(
			['mazes', 'motor', 'tesla', 'delivery'].flatMap((game) => [
				[`/${game}/graphql`, { target: process.env.DEV_BACKEND ?? 'http://localhost:8080', ws: true }],
				[`/${game}/auth`, { target: process.env.DEV_BACKEND ?? 'http://localhost:8080' }],
				[`/${game}/llms.txt`, { target: process.env.DEV_BACKEND ?? 'http://localhost:8080' }]
			])
		)
	},
	test: {
		environment: 'jsdom',
		setupFiles: ['./src/test/setup.ts'],
		include: ['src/**/*.{test,spec}.{js,ts}', 'packages/ui/**/*.{test,spec}.{js,ts}']
	}
} as UserConfig);
