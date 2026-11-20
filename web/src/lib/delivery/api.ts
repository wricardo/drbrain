// GraphQL client for the DrBrain Delivery API.
//
// GraphQL is the only transport: there is no REST surface. Types come from
// api-types.ts, which is generated from graph/schema.graphqls by `make types`,
// so the queries below are checked against the real schema at compile time.
//
// Each operation asks for exactly what a screen needs. Mutations return the full
// snapshot, so the client never needs a follow-up read to know the new state.

import type {
	Delivery_CarWindow as CarWindow,
	Delivery_Direction as Direction,
	Delivery_EventView as EventView,
	Delivery_Query as Query,
	Delivery_SessionSummary as SessionSummary,
	Delivery_Snapshot as Snapshot
} from '../graphql-types';

export * from '../graphql-types';

// Keep the delivery-local names the UI already uses.
export type {
	Delivery_AchievementView as AchievementView,
	Delivery_BuildView as BuildView,
	Delivery_CarState as CarState,
	Delivery_CarView as CarView,
	Delivery_CityView as CityView,
	Delivery_ClosureView as ClosureView,
	Delivery_Direction as Direction,
	Delivery_OrderState as OrderState,
	Delivery_OrderView as OrderView,
	Delivery_SessionSummary as SessionSummary,
	Delivery_Snapshot as Snapshot,
	Delivery_UnlockView as UnlockView
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
	const res = await fetch(ENDPOINT, {
		method: 'POST',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify({ query, variables })
	});

	if (!res.ok) {
		throw new ApiError(`HTTP ${res.status} ${res.statusText}`, 'http_error');
	}

	const body = (await res.json()) as GraphQLResponse<{ delivery?: T }>;
	if (body.errors && body.errors.length > 0) {
		const first = body.errors[0];
		const msg = first.message;
		// Resolver errors are "code: detail"; split so callers can branch.
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
`;

const ORDER_FIELDS = `
	id item itemName quantity
	kitchenId kitchenName customerId customerName
	state assignedCar
	createdTick deadlineTick ticksLeft
	reward paid late
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
		onTimeStreak tows buildsCompleted
		upkeepPaid towFees upkeepDue
		deliveriesByItem { item count }
	}
	carClasses { className price capacity maxBattery stepCost unlocked affordable }
	achievements { id title description done progress target }
	unlocks { id title description requires earned progress target builds }
	builds { id title description cost unlocked completed affordable applicable problem }
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

	/** The static city with every district revealed. */
	city: async () => {
		const d = await post<Pick<Query, 'city'>>(`query {
			delivery {
				city {
					name gridSize rows hiddenTiles
					kitchens { id name x y }
					customers { id name x y }
					depots { id name x y }
					chargers { id name x y }
				}
			}
		}`);
		return d.city;
	},

	sessions: async (): Promise<SessionSummary[]> => {
		const d = await post<{ sessions: SessionSummary[] }>(`query {
			delivery { sessions { id createdAt tick speed money reputation deliveries loaded } }
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

	createSession: async (id?: string, seed?: number): Promise<Snapshot> => {
		const d = await post<{ createSession: Snapshot }>(
			`mutation ($id: ID, $seed: Int) {
				delivery { createSession(id: $id, seed: $seed) { ${SNAPSHOT_FIELDS} } }
			}`,
			{ id: id ?? null, seed: seed ?? null }
		);
		return d.createSession;
	},

	/** Queue one step for one car. Does not advance the clock. */
	moveCar: async (id: string, carId: string, direction: Direction): Promise<Snapshot> => {
		const d = await post<{ moveCar: Snapshot }>(
			`mutation ($id: ID!, $carId: ID!, $direction: Delivery_Direction!) {
				delivery { moveCar(id: $id, carId: $carId, direction: $direction) { ${SNAPSHOT_FIELDS} } }
			}`,
			{ id, carId, direction }
		);
		return d.moveCar;
	},

	tick: async (id: string, count = 1): Promise<Snapshot> => {
		const d = await post<{ tick: Snapshot }>(
			`mutation ($id: ID!, $count: Int!) {
				delivery { tick(id: $id, count: $count) { ${SNAPSHOT_FIELDS} } }
			}`,
			{ id, count }
		);
		return d.tick;
	},

	setSpeed: async (id: string, speed: number): Promise<Snapshot> => {
		const d = await post<{ setSpeed: Snapshot }>(
			`mutation ($id: ID!, $speed: Int!) {
				delivery { setSpeed(id: $id, speed: $speed) { ${SNAPSHOT_FIELDS} } }
			}`,
			{ id, speed }
		);
		return d.setSpeed;
	},

	assignOrder: async (id: string, orderId: string, carId: string): Promise<Snapshot> => {
		const d = await post<{ assignOrder: Snapshot }>(
			`mutation ($id: ID!, $orderId: ID!, $carId: ID!) {
				delivery { assignOrder(id: $id, orderId: $orderId, carId: $carId) { ${SNAPSHOT_FIELDS} } }
			}`,
			{ id, orderId, carId }
		);
		return d.assignOrder;
	},

	unassignOrder: async (id: string, orderId: string): Promise<Snapshot> => {
		const d = await post<{ unassignOrder: Snapshot }>(
			`mutation ($id: ID!, $orderId: ID!) {
				delivery { unassignOrder(id: $id, orderId: $orderId) { ${SNAPSHOT_FIELDS} } }
			}`,
			{ id, orderId }
		);
		return d.unassignOrder;
	},

	buyCar: async (id: string, className: string, depotId = 'd_main'): Promise<Snapshot> => {
		const d = await post<{ buyCar: Snapshot }>(
			`mutation ($id: ID!, $className: String!, $depotId: String!) {
				delivery { buyCar(id: $id, className: $className, depotId: $depotId) { ${SNAPSHOT_FIELDS} } }
			}`,
			{ id, className, depotId }
		);
		return d.buyCar;
	},

	towCar: async (id: string, carId: string): Promise<Snapshot> => {
		const d = await post<{ towCar: Snapshot }>(
			`mutation ($id: ID!, $carId: ID!) {
				delivery { towCar(id: $id, carId: $carId) { ${SNAPSHOT_FIELDS} } }
			}`,
			{ id, carId }
		);
		return d.towCar;
	},

	build: async (id: string, buildId: string): Promise<Snapshot> => {
		const d = await post<{ build: Snapshot }>(
			`mutation ($id: ID!, $buildId: ID!) {
				delivery { build(id: $id, buildId: $buildId) { ${SNAPSHOT_FIELDS} } }
			}`,
			{ id, buildId }
		);
		return d.build;
	},

	saveSession: async (id: string): Promise<Snapshot> => {
		const d = await post<{ saveSession: Snapshot }>(
			`mutation ($id: ID!) { delivery { saveSession(id: $id) { ${SNAPSHOT_FIELDS} } } }`,
			{ id }
		);
		return d.saveSession;
	},

	deleteSession: async (id: string): Promise<boolean> => {
		const d = await post<{ deleteSession: boolean }>(
			`mutation ($id: ID!) { delivery { deleteSession(id: $id) } }`,
			{ id }
		);
		return d.deleteSession;
	}
};
