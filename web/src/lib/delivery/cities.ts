import type { CityInfo, CityStatus, CityView } from './api';

/** The catalogue fields that decide what a player may do with a city. */
export type CityAccess = Pick<CityInfo, 'status' | 'ownedByMe'> &
	Partial<Pick<CityInfo, 'builtin' | 'ownerless'>>;

/** Human label for the lifecycle enum. */
export function statusLabel(status: CityStatus): 'Draft' | 'Published' {
	return status === 'DRAFT' ? 'Draft' : 'Published';
}

/** A draft is visible only to its owner; the server already filtered the list. */
export function isDraft(city: CityAccess): boolean {
	return city.status === 'DRAFT';
}

export function isPublished(city: CityAccess): boolean {
	return city.status === 'PUBLISHED';
}

/** Published cities are playable by anyone; a draft only by its owner. */
export function canPlayCity(city: CityAccess): boolean {
	return isPublished(city) || city.ownedByMe;
}

/** Owners edit their own cities; shipped and legacy ownerless cities are the admin path. */
export function canEditCity(city: CityAccess): boolean {
	return city.ownedByMe || requiresAdminKey(city);
}

/** Shipped and legacy unowned cities need the admin key for edit/delete. */
export function requiresAdminKey(city: CityAccess): boolean {
	return !city.ownedByMe && (city.builtin === true || city.ownerless === true);
}

/** Only an owner publishes, and only from DRAFT. */
export function canPublishCity(city: CityAccess): boolean {
	return city.ownedByMe && isDraft(city);
}

/** Owners may delete their own city; shipped/legacy rows are the admin path. */
export function canDeleteCity(city: CityAccess): boolean {
	return city.ownedByMe || requiresAdminKey(city);
}

/** Stable id order for a picker or catalogue grid. */
export function sortCities<T extends { id: string }>(cities: T[]): T[] {
	return [...cities].sort((a, b) => a.id.localeCompare(b.id));
}

/** Pretty JSON for the authoring textarea; empty string for unserialisable input. */
export function formatCityJSON(value: unknown): string {
	return JSON.stringify(value, null, 2) ?? '';
}

/** Parse editor text without throwing, so the textarea can retain the user's input. */
export function parseCityJSON(text: string): { value: unknown; error: null } | { value: null; error: string } {
	try {
		return { value: JSON.parse(text), error: null };
	} catch (error) {
		return { value: null, error: error instanceof Error ? error.message : String(error) };
	}
}

/** Pretty-print a server-returned config document; keep malformed input as-is. */
export function formatCityConfigText(raw: string): string {
	const parsed = parseCityJSON(raw);
	return parsed.error ? raw : formatCityJSON(parsed.value);
}

/**
 * A small, fully valid starter city. The id is the API handle; the display name
 * is deliberately separate so renaming a city does not move it.
 */
export function starterCityConfig(name = 'New city'): Record<string, unknown> {
	return {
		name,
		grid_size: 5,
		layout: ['.....', '.....', '..D..', '.....', '.....'],
		legend: { '.': 'road', D: 'depot' },
		items: {},
		kitchens: [],
		customers: [],
		depots: [{ id: 'home', pos: { x: 2, y: 2 }, capacity: 4 }],
		chargers: [],
		initial_cars: [],
		car_classes: {},
		districts: {},
		orders: {},
		charging: {},
		growth: []
	};
}

/**
 * Best-effort CityConfig from a rendered CityView.
 *
 * The API returns the static map, not the original config document, so the
 * simulation tables (items, menus, demands, orders, growth) cannot be recovered
 * from it. This seeds the editor with the layout/entities for authoring; the
 * UI tells the author the converted document needs review before publishing.
 */
export function cityConfigFromView(view: CityView): Record<string, unknown> {
	const legend: Record<string, string> = {
		'.': 'road',
		',': 'ground',
		'#': 'water',
		B: 'building',
		D: 'depot',
		C: 'charger',
		K: 'kitchen',
		h: 'house',
		H: 'house'
	};
	const layout = view.rows.map((row) => row.replaceAll('h', 'H'));
	return {
		name: view.name,
		grid_size: view.gridSize,
		layout,
		legend,
		items: {},
		kitchens: view.kitchens.map((p) => ({
			id: p.id,
			name: p.name,
			pos: { x: p.x, y: p.y },
			menu: []
		})),
		customers: view.customers.map((p) => ({
			id: p.id,
			name: p.name,
			pos: { x: p.x, y: p.y },
			demands: [],
			order_interval_ticks: 1,
			jitter_ticks: 0
		})),
		depots: view.depots.map((p) => ({
			id: p.id,
			pos: { x: p.x, y: p.y },
			capacity: 1
		})),
		chargers: view.chargers.map((p) => ({
			id: p.id,
			pos: { x: p.x, y: p.y },
			capacity: 1
		})),
		initial_cars: [],
		car_classes: {},
		districts: {},
		orders: {},
		charging: {},
		growth: []
	};
}
