import { describe, expect, it } from 'vitest';
import {
	canDeleteCity,
	canEditCity,
	canPlayCity,
	canPublishCity,
	cityConfigFromView,
	formatCityConfigText,
	formatCityJSON,
	isDraft,
	isPublished,
	parseCityJSON,
	requiresAdminKey,
	sortCities,
	starterCityConfig,
	statusLabel
} from './cities';
import type { CityView } from './api';

const published = { status: 'PUBLISHED' as const, ownedByMe: false };
const builtinPublished = { status: 'PUBLISHED' as const, ownedByMe: false, builtin: true, ownerless: false };
const legacyPublished = { status: 'PUBLISHED' as const, ownedByMe: false, builtin: false, ownerless: true };
const ownDraft = { status: 'DRAFT' as const, ownedByMe: true };
const ownPublished = { status: 'PUBLISHED' as const, ownedByMe: true };

describe('city status and ownership helpers', () => {
	it('labels the lifecycle enum', () => {
		expect(statusLabel('DRAFT')).toBe('Draft');
		expect(statusLabel('PUBLISHED')).toBe('Published');
	});

	it('recognises drafts and published cities', () => {
		expect(isDraft(ownDraft)).toBe(true);
		expect(isPublished(published)).toBe(true);
	});

	it('only lets an owner play their own draft', () => {
		expect(canPlayCity(published)).toBe(true);
		expect(canPlayCity(ownPublished)).toBe(true);
		expect(canPlayCity(ownDraft)).toBe(true);
		expect(canPlayCity({ status: 'DRAFT', ownedByMe: false })).toBe(false);
	});

	it('separates owner edits from the admin path', () => {
		expect(canEditCity(ownDraft)).toBe(true);
		expect(canEditCity(published)).toBe(false); // another player's owned city: no admin affordance
		expect(requiresAdminKey(published)).toBe(false);
		expect(canEditCity(builtinPublished)).toBe(true);
		expect(requiresAdminKey(builtinPublished)).toBe(true);
		expect(canEditCity(legacyPublished)).toBe(true);
		expect(requiresAdminKey(legacyPublished)).toBe(true);
		expect(requiresAdminKey(ownPublished)).toBe(false);
		expect(requiresAdminKey(ownDraft)).toBe(false);
	});

	it('publishes only an owner draft', () => {
		expect(canPublishCity(ownDraft)).toBe(true);
		expect(canPublishCity(ownPublished)).toBe(false);
		expect(canPublishCity(published)).toBe(false);
	});

	it('deletes owner cities and leaves shipped/legacy rows to the admin path', () => {
		expect(canDeleteCity(ownDraft)).toBe(true);
		expect(canDeleteCity(ownPublished)).toBe(true);
		expect(canDeleteCity(published)).toBe(false);
		expect(canDeleteCity(builtinPublished)).toBe(true);
		expect(canDeleteCity(legacyPublished)).toBe(true);
	});

	it('sorts by id without mutating the input', () => {
		const list = [{ id: 'zulu' }, { id: 'alpha' }];
		expect(sortCities(list).map((c) => c.id)).toEqual(['alpha', 'zulu']);
		expect(list.map((c) => c.id)).toEqual(['zulu', 'alpha']);
	});
});

describe('city JSON helpers', () => {
	it('pretty prints config documents', () => {
		expect(formatCityJSON({ name: 'A', grid_size: 5 })).toBe('{\n  "name": "A",\n  "grid_size": 5\n}');
	});

	it('reports malformed JSON without throwing', () => {
		const bad = parseCityJSON('{');
		expect(bad.error).toBeTruthy();
		expect(bad.value).toBeNull();
		const good = parseCityJSON('{"name":"A"}');
		expect(good.error).toBeNull();
		expect(good.value).toEqual({ name: 'A' });
	});

	it('pretty prints a raw server config document', () => {
		expect(formatCityConfigText('{"name":"A","grid_size":5}')).toBe('{\n  "name": "A",\n  "grid_size": 5\n}');
		expect(formatCityConfigText('{')).toBe('{');
	});

	it('provides a small valid starter document', () => {
		const cfg = starterCityConfig('Starter');
		expect(cfg.name).toBe('Starter');
		expect(cfg.grid_size).toBe(5);
		expect((cfg.depots as unknown[]).length).toBe(1);
		expect(cfg.charging).toMatchObject({ max_purchased_chargers: 6 });
	});
});

describe('cityConfigFromView', () => {
	const view: CityView = {
		id: 'demo',
		name: 'Demo',
		gridSize: 5,
		status: 'DRAFT',
		ownedByMe: true,
		builtin: false,
		ownerless: false,
		rows: ['.....', '..K..', '..D..', '..H..', 'h....'],
		hiddenTiles: 0,
		kitchens: [{ id: 'k1', name: 'Kitchen', x: 2, y: 1 }],
		customers: [{ id: 'c1', name: 'Customer', x: 2, y: 3 }],
		depots: [{ id: 'home', name: '', x: 2, y: 2 }],
		chargers: [],
		charging: {
			depotRate: 6,
			chargerRate: 2,
			chargerPricePerTick: 1,
			chargerPurchasePrice: 150,
			maxPurchasedChargers: 6
		}
	};

	it('keeps the layout, normalises house glyphs, and maps entities', () => {
		const cfg = cityConfigFromView(view);
		expect(cfg.name).toBe('Demo');
		expect(cfg.grid_size).toBe(5);
		expect((cfg.layout as string[])[4]).toBe('H....');
		expect((cfg.kitchens as { id: string }[])[0].id).toBe('k1');
		expect((cfg.customers as { id: string }[])[0].id).toBe('c1');
		expect((cfg.depots as { id: string }[])[0].id).toBe('home');
	});

	it('carries the charging economy through to the authored config', () => {
		const cfg = cityConfigFromView(view);
		expect(cfg.charging).toEqual({
			depot_rate: 6,
			charger_rate: 2,
			charger_price_per_tick: 1,
			charger_purchase_price: 150,
			max_purchased_chargers: 6
		});
	});

	it('round-trips through the pretty-printer', () => {
		const text = formatCityJSON(cityConfigFromView(view));
		const parsed = parseCityJSON(text);
		expect(parsed.error).toBeNull();
	});
});
