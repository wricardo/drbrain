import * as child_process from 'node:child_process';
import adapter from '@sveltejs/adapter-static';

// SvelteKit defaults the app version to a build timestamp, which feeds every
// asset hash. That made two builds of identical sources differ, so rebuilding
// the same commit rewrote all of static/. Use the commit instead: rebuilds are
// reproducible and a dirty static/ means the sources really changed.
function commitVersion() {
	try {
		return child_process.execSync('git rev-parse HEAD', { stdio: ['ignore', 'pipe', 'ignore'] }).toString().trim();
	} catch {
		return 'dev';
	}
}

/** @type {import('@sveltejs/kit').Config} */
const config = {
	compilerOptions: {
		// Force runes mode for the project, except for libraries. Can be removed in svelte 6.
		runes: ({ filename }) => (filename.split(/[/\\]/).includes('node_modules') ? undefined : true)
	},
	kit: {
		version: { name: commitVersion() },
		// adapter-auto only supports some environments, see https://svelte.dev/docs/kit/adapter-auto for a list.
		// If your environment is not supported, or you settled on a specific environment, switch out the adapter.
		// See https://svelte.dev/docs/kit/adapters for more information about adapters.
		adapter: adapter({
			pages: 'build',
			assets: 'build',
			fallback: 'index.html',
			precompress: false,
			strict: false
		})
	}
};

export default config;
