import { sveltekit } from '@sveltejs/kit/vite';
import tailwindcss from '@tailwindcss/vite';
import { defineConfig } from 'vite';

// The Go server the dev UI talks to. Override with API_URL when :8080 is
// taken (another project's server, a different checkout) so the proxy does
// not silently point at the wrong process.
const API_URL = process.env.API_URL ?? 'http://localhost:8080';

export default defineConfig(({ command }) => {
	// Say which server the dev UI proxies to. The failure this prevents is
	// silent: a proxy aimed at the wrong process (or at an older build of the
	// right one) surfaces as GraphQL validation errors in the browser, which
	// look like frontend bugs. Print on serve only — `npm run build` writes
	// static files and proxies nothing, so a line there would just be noise.
	if (command === 'serve') {
		console.log(`[vite] proxying /graphql -> ${API_URL}`);
	}

	return {
		plugins: [tailwindcss(), sveltekit()],
		server: {
			// During local development the API runs on :8080 while Vite serves
			// the UI on :5173, so proxy the GraphQL endpoint instead of dealing
			// with CORS. The prefix must match ENDPOINT in src/lib/api.ts: there
			// is no REST surface, so proxying "/api" left every request 404ing.
			// "/graphql" also covers the playground at /graphql/playground.
			proxy: {
				'/graphql': { target: API_URL }
			},
			// `build/` is the adapter-static output. Vite otherwise watches it,
			// so running `npm run build` while `npm run dev` is up reloads the
			// page dozens of times as the bundle is rewritten.
			watch: { ignored: ['**/build/**'] }
		}
	};
});
