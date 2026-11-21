// GraphQL client for the DrBrain Delivery API.
//
// GraphQL is the only transport: there is no REST surface. Types come from
// api-types.ts, which is generated from graph/schema.graphqls by `make types`,
// so the queries below are checked against the real schema at compile time.
//
// Each operation asks for exactly what a screen needs. Mutations return the full
// snapshot, so the client never needs a follow-up read to know the new state.

import { authHeaders } from '$lib/account/token';
import { getAdminKey } from './admin';
import { forgetOwnerTokens, ownerTokenFor, rememberSession } from './ownerTokens';
import type {
	Delivery_CarWindow as CarWindow,
	Delivery_ClaimStatus as ClaimStatus,
	Delivery_CityInfo as CityInfo,
	Delivery_CityValidationResult as CityValidationResult,
	Delivery_CityView as CityView,
	Delivery_Direction as Direction,
	Delivery_EventView as EventView,
	Delivery_SessionSummary as SessionSummary,
	Delivery_Snapshot as Snapshot
} from '../graphql-types';

export * from '../graphql-types';

// Keep the delivery-local names the UI already uses.
export type {
	Delivery_CarState as CarState,
	Delivery_GrowthView as GrowthView,
	Delivery_CarView as CarView,
	Delivery_CashEntryView as CashEntryView,
	Delivery_CashKind as CashKind,
	Delivery_CashCategory as CashCategory,
	Delivery_MonthView as MonthView,
	Delivery_MonthCategoryView as MonthCategoryView,
	Delivery_CityInfo as CityInfo,
	Delivery_CityStatus as CityStatus,
	Delivery_CityValidationResult as CityValidationResult,
	Delivery_CityView as CityView,
	Delivery_ClosureView as ClosureView,
	Delivery_Direction as Direction,
	Delivery_OrderState as OrderState,
	Delivery_OrderView as OrderView,
	Delivery_SessionSummary as SessionSummary,
	Delivery_Snapshot as Snapshot
} from '../graphql-types';

const ENDPOINT = '/graphql';

/** ApiError carries the server's machine-readable code, not just prose. */
export class ApiError extends Error {
	readonly code: string;

	constructor(message: string, code: string) {
		super(message);
		this.name = 'ApiError';
		this.code = code;
	}
}

/** GraphQL error envelope. */
interface GraphQLResponse<T> {
	data?: T;
	errors?: { message: string; extensions?: Record<string, unknown> }[];
}

/**
 * post sends one operation and unwraps the GraphQL envelope.
 *
 * Errors are surfaced as ApiError with a code, because the resolvers prefix
 * messages with one ("not_found: …"). That keeps client branching possible
 * without inventing an extensions scheme the schema does not declare.
 */
async function post<T>(query: string, variables?: Record<string, unknown>): Promise<T> {
	const adminKey = getAdminKey();
	const res = await fetch(ENDPOINT, {
		method: 'POST',
		headers: {
			'Content-Type': 'application/json',
			...authHeaders(),
			...(adminKey ? { 'X-Admin-Key': adminKey } : {})
		},
		body: JSON.stringify({ query, variables })
	});

	if (!res.ok) {
		throw new ApiError(`HTTP ${res.status} ${res.statusText}`, 'http_error');
	}

	const body = (await res.json()) as GraphQLResponse<{ delivery?: T }>;
	if (body.errors && body.errors.length > 0) {
		const first = body.errors[0];
		const msg = first.message;
		// Newer city resolvers put the code in extensions; older session
		// resolvers prefix the message with it. Support both.
		const extensionCode = first.extensions?.code;
		if (typeof extensionCode === 'string' && extensionCode) {
			throw new ApiError(msg, extensionCode);
		}
		const idx = msg.indexOf(':');
		if (idx > 0 && idx < 32) {
			throw new ApiError(msg.slice(idx + 1).trim(), msg.slice(0, idx).trim());
		}
		throw new ApiError(msg, 'graphql_error');
	}
	const data = body.data?.delivery;
	if (!data) {
		throw new ApiError('empty GraphQL response', 'empty_response');
	}
	return data;
}

// --- field selections ------------------------------------------------------
//
// Kept as constants so a schema rename is a single edit, and so the queries stay
// readable. The fragment shape mirrors the generated Snapshot type.

const CAR_FIELDS = `
	id name className x y
	battery maxBattery batteryPct capacity load freeBoxes
	cargo state queuedMove lastReject
	chargeDue
`;

const ORDER_FIELDS = `
	id item itemName quantity
	kitchenId kitchenName customerId customerName
	state assignedCar
	createdTick deadlineTick ticksLeft
	reward paid late
`;

/** CITY_VIEW_FIELDS is the metadata plus static map for one catalogue city. */
export const CITY_VIEW_FIELDS = `
	id name gridSize status ownedByMe builtin ownerless rows hiddenTiles
	kitchens { id name x y }
	customers { id name x y }
	depots { id name x y }
	chargers { id name x y }
`;

/**
 * SNAPSHOT_FIELDS is the field selection for one session snapshot.
 *
 * Exported because the subscription must ask for exactly the same shape as the
 * query: a stream that omitted a field the page renders would leave that field
 * undefined only after the first pushed frame replaced the fetched snapshot.
 */
export const SNAPSHOT_FIELDS = `
	id createdAt tick speed paused tickMs
	city {
		name gridSize rows hiddenTiles
		kitchens { id name x y }
		customers { id name x y }
		depots { id name x y }
		chargers { id name x y }
	}
	cars { ${CAR_FIELDS} }
	orders { ${ORDER_FIELDS} }
	ledger {
		money moneyEarned reputation
		deliveries lateDeliveries expiredOrders ordersSpawned
		onTimeStreak tows
		upkeepPaid towFees upkeepDue
		ticksPerMonth
		cashEntries { seq tick kind category amount balance carId carName orderId memo }
		months {
			index startTick endTick income expenses net
			categories { category income expenses }
		}
		deliveriesByItem { item count }
	}
	carClasses { className price capacity maxBattery stepCost unlocked affordable }
	growth { id tick title description done }
	events { tick type message carId x y }
	closures { id reason startTick endTick ticksLeft minX minY maxX maxY tiles { x y } }
`;

export const api = {
	/** Liveness probe. */
	health: async () => {
		const d = await post<{ health: { status: string } }>(
			`query { delivery { health { status } } }`
		);
		return d.health;
	},

	/**
	 * The exact authored city JSON (decoded/defaulted server-side) for a
	 * published city, a builtin, or the caller's own draft. Hidden or missing
	 * cities return null.
	 */
	cityConfig: async (id: string): Promise<string | null> => {
		const d = await post<{ cityConfig: string | null }>(
			`query ($id: ID!) { delivery { cityConfig(id: $id) } }`,
			{ id }
		);
		return d.cityConfig;
	},

	/** The city catalogue: published cities plus the caller's own drafts. */
	cities: async (): Promise<CityInfo[]> => {
		const d = await post<{ cities: CityInfo[] }>(`query {
			delivery { cities { id name gridSize status ownedByMe builtin ownerless } }
		}`);
		return d.cities ?? [];
	},

	/**
	 * One city's static map and metadata. Omitted id selects the server's
	 * default city. Missing cities and other players' drafts return null.
	 */
	city: async (id?: string): Promise<CityView | null> => {
		const d = await post<{ city: CityView | null }>(
			`query ($id: ID) { delivery { city(id: $id) { ${CITY_VIEW_FIELDS} } } }`,
			{ id: id ?? null }
		);
		return d.city;
	},

	sessions: async (): Promise<SessionSummary[]> => {
		const d = await post<{ sessions: SessionSummary[] }>(`query {
			delivery { sessions { id createdAt tick speed money reputation deliveries loaded ownerDisplayName ownerHandle mine } }
		}`);
		return d.sessions ?? [];
	},

	session: async (id: string): Promise<Snapshot> => {
		const d = await post<{ session: Snapshot }>(
			`query ($id: ID!) { delivery { session(id: $id) { ${SNAPSHOT_FIELDS} } } }`,
			{ id }
		);
		return d.session;
	},

	/** A window of the city around one car: the cheap poll. */
	carView: async (id: string, carId: string, radius = 2): Promise<CarWindow> => {
		const d = await post<{ carView: CarWindow }>(
			`query ($id: ID!, $carId: ID!, $radius: Int!) {
				delivery {
					carView(id: $id, carId: $carId, radius: $radius) {
						rowSpan colSpan rows originX originY
						car { ${CAR_FIELDS} }
						visibleCars { ${CAR_FIELDS} }
						nearbyOrders { ${ORDER_FIELDS} }
					}
				}
			}`,
			{ id, carId, radius }
		);
		return d.carView;
	},

	/** Events newer than a tick, for incremental polling. */
	events: async (id: string, sinceTick = 0): Promise<EventView[]> => {
		const d = await post<{ events: EventView[] }>(
			`query ($id: ID!, $sinceTick: Int!) {
				delivery { events(id: $id, sinceTick: $sinceTick) { tick type message carId x y } }
			}`,
			{ id, sinceTick }
		);
		return d.events ?? [];
	},

	// --- mutations ---------------------------------------------------------

	/** Creates a session on a city (omitted = server default) and keeps the owner token. */
	createSession: async (id?: string, seed?: number, cityId?: string): Promise<Snapshot> => {
		const d = await post<{ createSession: { session: Snapshot; ownerToken: string } }>(
			`mutation ($id: ID, $seed: Int, $cityId: ID) {
				delivery { createSession(id: $id, seed: $seed, cityId: $cityId) { session { ${SNAPSHOT_FIELDS} } ownerToken } }
			}`,
			{ id: id ?? null, seed: seed ?? null, cityId: cityId ?? null }
		);
		const { session, ownerToken } = d.createSession;
		rememberSession(session.id, ownerToken);
		return session;
	},

	/** Saves a new owned DRAFT city. config is raw city JSON. */
	createCity: async (id: string, config: string): Promise<CityView> => {
		const d = await post<{ createCity: CityView }>(
			`mutation ($id: ID!, $config: String!) {
				delivery { createCity(id: $id, config: $config) { ${CITY_VIEW_FIELDS} } }
			}`,
			{ id, config }
		);
		return d.createCity;
	},

	/** Replaces an owned city (or a shipped/legacy city with an admin key); owners get DRAFT back. */
	updateCity: async (id: string, config: string): Promise<CityView> => {
		const d = await post<{ updateCity: CityView }>(
			`mutation ($id: ID!, $config: String!) {
				delivery { updateCity(id: $id, config: $config) { ${CITY_VIEW_FIELDS} } }
			}`,
			{ id, config }
		);
		return d.updateCity;
	},

	/** Publishes an owner's draft after full validation. */
	publishCity: async (id: string): Promise<CityView> => {
		const d = await post<{ publishCity: CityView }>(
			`mutation ($id: ID!) { delivery { publishCity(id: $id) { ${CITY_VIEW_FIELDS} } } }`,
			{ id }
		);
		return d.publishCity;
	},

	/** Deletes an owned city (or a shipped/legacy city with an admin key). */
	deleteCity: async (id: string): Promise<boolean> => {
		const d = await post<{ deleteCity: boolean }>(
			`mutation ($id: ID!) { delivery { deleteCity(id: $id) } }`,
			{ id }
		);
		return d.deleteCity;
	},

	/** Checks raw city JSON without storing it. */
	validateCity: async (config: string): Promise<CityValidationResult> => {
		const d = await post<{ validateCity: CityValidationResult }>(
			`mutation ($config: String!) { delivery { validateCity(config: $config) { valid publishable issues } } }`,
			{ config }
		);
		return d.validateCity;
	},

	/** Queue one step for one car. Does not advance the clock. */
	moveCar: async (id: string, carId: string, direction: Direction): Promise<Snapshot> => {
		const d = await post<{ moveCar: Snapshot }>(
			`mutation ($id: ID!, $ownerToken: String, $carId: ID!, $direction: Delivery_Direction!) {
				delivery { moveCar(id: $id, ownerToken: $ownerToken, carId: $carId, direction: $direction) { ${SNAPSHOT_FIELDS} } }
			}`,
			{ id, ownerToken: ownerTokenFor(id), carId, direction }
		);
		return d.moveCar;
	},

	tick: async (id: string, count = 1): Promise<Snapshot> => {
		const d = await post<{ tick: Snapshot }>(
			`mutation ($id: ID!, $ownerToken: String, $count: Int!) {
				delivery { tick(id: $id, ownerToken: $ownerToken, count: $count) { ${SNAPSHOT_FIELDS} } }
			}`,
			{ id, ownerToken: ownerTokenFor(id), count }
		);
		return d.tick;
	},

	setSpeed: async (id: string, speed: number): Promise<Snapshot> => {
		const d = await post<{ setSpeed: Snapshot }>(
			`mutation ($id: ID!, $ownerToken: String, $speed: Int!) {
				delivery { setSpeed(id: $id, ownerToken: $ownerToken, speed: $speed) { ${SNAPSHOT_FIELDS} } }
			}`,
			{ id, ownerToken: ownerTokenFor(id), speed }
		);
		return d.setSpeed;
	},

	assignOrder: async (id: string, orderId: string, carId: string): Promise<Snapshot> => {
		const d = await post<{ assignOrder: Snapshot }>(
			`mutation ($id: ID!, $ownerToken: String, $orderId: ID!, $carId: ID!) {
				delivery { assignOrder(id: $id, ownerToken: $ownerToken, orderId: $orderId, carId: $carId) { ${SNAPSHOT_FIELDS} } }
			}`,
			{ id, ownerToken: ownerTokenFor(id), orderId, carId }
		);
		return d.assignOrder;
	},

	unassignOrder: async (id: string, orderId: string): Promise<Snapshot> => {
		const d = await post<{ unassignOrder: Snapshot }>(
			`mutation ($id: ID!, $ownerToken: String, $orderId: ID!) {
				delivery { unassignOrder(id: $id, ownerToken: $ownerToken, orderId: $orderId) { ${SNAPSHOT_FIELDS} } }
			}`,
			{ id, ownerToken: ownerTokenFor(id), orderId }
		);
		return d.unassignOrder;
	},

	buyCar: async (id: string, className: string, depotId = 'd_main'): Promise<Snapshot> => {
		const d = await post<{ buyCar: Snapshot }>(
			`mutation ($id: ID!, $ownerToken: String, $className: String!, $depotId: String!) {
				delivery { buyCar(id: $id, ownerToken: $ownerToken, className: $className, depotId: $depotId) { ${SNAPSHOT_FIELDS} } }
			}`,
			{ id, ownerToken: ownerTokenFor(id), className, depotId }
		);
		return d.buyCar;
	},

	towCar: async (id: string, carId: string): Promise<Snapshot> => {
		const d = await post<{ towCar: Snapshot }>(
			`mutation ($id: ID!, $ownerToken: String, $carId: ID!) {
				delivery { towCar(id: $id, ownerToken: $ownerToken, carId: $carId) { ${SNAPSHOT_FIELDS} } }
			}`,
			{ id, ownerToken: ownerTokenFor(id), carId }
		);
		return d.towCar;
	},

	saveSession: async (id: string): Promise<Snapshot> => {
		const d = await post<{ saveSession: Snapshot }>(
			`mutation ($id: ID!, $ownerToken: String) { delivery { saveSession(id: $id, ownerToken: $ownerToken) { ${SNAPSHOT_FIELDS} } } }`,
			{ id, ownerToken: ownerTokenFor(id) }
		);
		return d.saveSession;
	},

	deleteSession: async (id: string): Promise<boolean> => {
		const d = await post<{ deleteSession: boolean }>(
			`mutation ($id: ID!, $ownerToken: String) { delivery { deleteSession(id: $id, ownerToken: $ownerToken) } }`,
			{ id, ownerToken: ownerTokenFor(id) }
		);
		if (d.deleteSession) forgetOwnerTokens([id]);
		return d.deleteSession;
	},

	/** Whether this browser may change the session; false means spectate. Never an error. */
	canPlay: async (id: string): Promise<boolean> => {
		const d = await post<{ canPlaySession: boolean }>(
			`query ($id: ID!, $ownerToken: String) { delivery { canPlaySession(id: $id, ownerToken: $ownerToken) } }`,
			{ id, ownerToken: ownerTokenFor(id) }
		);
		return d.canPlaySession;
	},

	/** Attaches this browser's guest sessions to the signed-in account. */
	claimSessions: async (claims: { sessionID: string; ownerToken: string }[]): Promise<{ sessionID: string; status: ClaimStatus }[]> => {
		const d = await post<{ claimSessions: { sessionID: string; status: ClaimStatus }[] }>(
			`mutation ($claims: [Delivery_SessionClaimInput!]!) { delivery { claimSessions(claims: $claims) { sessionID status } } }`,
			{ claims }
		);
		return d.claimSessions ?? [];
	}
};
