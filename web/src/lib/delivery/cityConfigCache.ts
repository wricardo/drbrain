// The city query returns a rendered CityView, not the original config document.
// Keep the JSON this browser last saved/loaded for a city so reopening the
// editor starts from the authored knobs rather than a lossy reconstruction.

const PREFIX = 'drbrain-delivery.cityConfig.';

function key(id: string): string {
	return `${PREFIX}${id}`;
}

export function readCachedCityConfig(id: string): string | null {
	try {
		return globalThis.localStorage?.getItem(key(id)) ?? null;
	} catch {
		return null;
	}
}

export function writeCachedCityConfig(id: string, config: string): void {
	try {
		globalThis.localStorage?.setItem(key(id), config);
	} catch {
		// storage unavailable: the editor still works for this page load
	}
}

export function clearCachedCityConfig(id: string): void {
	try {
		globalThis.localStorage?.removeItem(key(id));
	} catch {
		// ignore
	}
}
