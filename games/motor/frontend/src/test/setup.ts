import '@testing-library/jest-dom/vitest';

// Node 22+ defines a `localStorage` global that stays undefined unless the
// process runs with --localstorage-file. Vitest only copies a jsdom window key
// onto the test global when the key is absent, so Node's stub shadows jsdom's
// real storage and `localStorage` is undefined in tests (same for
// sessionStorage when Node exposes it). Restore jsdom's instances, which vitest
// keeps as `_localStorage` / `_sessionStorage`.
for (const key of ['localStorage', 'sessionStorage'] as const) {
	const g = globalThis as Record<string, unknown>;
	if (g[key] !== undefined) continue;
	const jsdomStorage = g[`_${key}`];
	if (jsdomStorage === undefined) continue;
	Object.defineProperty(globalThis, key, { value: jsdomStorage, writable: true, configurable: true });
}
