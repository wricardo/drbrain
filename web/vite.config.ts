import { sveltekit } from '@sveltejs/kit/vite';
import tailwindcss from '@tailwindcss/vite';
import { defineConfig, type UserConfig } from 'vite';

// The `test` block is read by vitest at runtime. vitest ships its own vite typings
// (a different major than the app's vite), so the config is asserted as a plain vite UserConfig.
// One backend for the whole app; override for a deployed host.
const backend = process.env.DEV_BACKEND ?? 'http://localhost:8080';

export default defineConfig({
	plugins: [tailwindcss(), sveltekit()],
	resolve: {
		conditions: ['browser']
	},
	server: {
		// All games share the one namespaced GraphQL endpoint; the remaining
		// per-game routes below are non-GraphQL APIs.
		proxy: {
			'/graphql': { target: backend, ws: true },
			'/mazes/llms.txt': { target: backend },
			'/motor/auth': { target: backend },
			'/motor/llms.txt': { target: backend },
			'/tesla/mcp': { target: backend },
			'/tesla/llms.txt': { target: backend },
			'/delivery/llms.txt': { target: backend }
		}
	},
	test: {
		environment: 'jsdom',
		setupFiles: ['./src/test/setup.ts'],
		include: ['src/**/*.{test,spec}.{js,ts}', 'packages/ui/**/*.{test,spec}.{js,ts}']
	}
} as UserConfig);
