/** The slice of a delivery snapshot the prompt needs. Kept structural so a full Snapshot passes. */
export interface PromptSession {
	id: string;
}

export interface PromptOptions {
	/**
	 * The secret returned by createSession for this browser. Null when this
	 * browser is watching someone else's city; the prompt must not invent one.
	 */
	ownerToken?: string | null;
	/** False means the page is read-only. Defaults to true. */
	canPlay?: boolean;
}

/**
 * Builds the prompt a player copies into an AI chat to play one existing
 * Delivery session through GraphQL. It carries only what is needed to reach
 * and authenticate against the session (id, endpoint, token), a short manual,
 * and the operation list; it deliberately omits the current game state so the
 * agent reads that from the API itself.
 *
 * The owner token is a credential. A read-only/spectator prompt never includes
 * one, and the account-owned case tells the agent to ask for a bearer token
 * instead of printing the account credential into the page.
 */
export function buildSessionPrompt(origin: string, s: PromptSession, opts: PromptOptions = {}): string {
	const ownerToken = opts.ownerToken?.trim() || null;
	const canPlay = opts.canPlay ?? true;
	const tokenArg = ownerToken ? `, ownerToken: "${ownerToken}"` : '';
	const ownership = ownerToken
		? `Owner token (secret; keep it private and send it on every mutation below): ${ownerToken}`
		: canPlay
			? `This city is playable from my browser, but this page holds no owner token. If it belongs to my drbrain account, mutations need that account's bearer token in the Authorization header; a session created before owner tokens may need no credential. I will provide the token when needed. Do not ask for an owner token.`
			: `Read-only view: this browser does not hold this city's owner token or owning-account credential. You can read the session, but mutations will be FORBIDDEN. Ask me for the credential before trying to change it.`;
	const authNote = ownerToken
		? `The ownerToken above is the credential. Every mutation below accepts it as ownerToken; without it the server answers FORBIDDEN.`
		: canPlay
			? `If this city belongs to my account, send the account bearer token as the request header \`Authorization: Bearer <token>\` on every request. If it is a legacy open session, mutations need no credential.`
			: `This is a read-only session for this browser; do not attempt mutations.`;

	return `Use the DrBrain Delivery GraphQL API to play existing session ${s.id}.

Session ID: ${s.id}
${ownership}
GraphQL endpoint: ${origin}/graphql
GraphQL Playground: ${origin}/graphql/playground
Full rules and every field: ${origin}/delivery/llms.txt
Watch this city live: ${origin}/delivery?city=${encodeURIComponent(s.id)}

${authNote}

## Explore the schema first
Introspection is enabled and the SDL descriptions are the source of truth. At the Playground or endpoint run \`{ __schema { types { name } } }\` / \`{ __type(name: "Delivery_Mutation") { fields { name args { name } } } }\` whenever a field, argument, enum or return shape is unclear. Everything is under the \`delivery\` namespace, so all operations look like \`{ delivery { ... } }\`.

Main queries:
- \`session(id: ID!): Delivery_Snapshot!\` — full current state: tick, speed, city view, cars, orders/openOrders, ledger, carClasses, growth, events, closures.
- \`carView(id: ID!, carId: ID!, radius: Int = 2): Delivery_CarWindow!\` — cheap local window around one car; radius is clamped to 10.
- \`events(id: ID!, sinceTick: Int = 0, limit: Int = 200): Delivery_EventFeed!\` — deltas by tick, with \`oldestTick\`/\`truncated\` for gaps.
- \`canPlaySession(id: ID!, ownerToken: String): Boolean!\` — whether this caller may mutate; never errors, false for unknown sessions.
- \`cities\`, \`city(id)\`, \`sessions(...)\` — optional discovery of cities and saves.

Subscription root \`deliverySession(id: ID!): Delivery_Snapshot!\` streams snapshots when the client supports graphql-ws.

Main mutations (all creator-only, all return the updated \`Delivery_Snapshot\` unless noted):
- \`assignOrder(id, orderId, carId, ownerToken)\` — point an order at a car and refresh its deadline from that car's position.
- \`unassignOrder(id, orderId, ownerToken)\` — return the order to PENDING.
- \`moveCar(id, carId, direction: NORTH|SOUTH|EAST|WEST, ownerToken)\` — append one step to the car's queue; does not advance the clock.
- \`clearMoves(id, carId, ownerToken)\` — drop the whole queued route.
- \`tick(id, count: Int = 1, ownerToken)\` — advance the world manually; one queued step runs per tick.
- \`setSpeed(id, speed: Int, ownerToken)\` — clock multiplier; 0 pauses.
- \`waitUntil(id, tick, ticks, ownerToken)\` — block until the running clock reaches an absolute future \`tick\` or \`ticks\` from now (max 1000); pass exactly one. Fails with INVALID_ARGUMENT if the session is paused, including when paused mid-wait; use \`tick\` to advance without a clock.
- \`towCar(id, carId, ownerToken)\` — return a stranded car to its nearest depot for a fee.
- \`buyCar(id, className, depotId = "d_main", ownerToken)\` — buy a vehicle at a depot.
- \`saveSession(id, ownerToken)\`, \`renameSession(id, displayName, ownerToken)\` — save now / name the save.
- \`evictSession(id, ownerToken)\`, \`deleteSession(id, ownerToken)\` — save-then-unload / delete permanently; both return Boolean, not a snapshot.
- \`createSession(id, seed, cityId)\` — start a new game; returns the secret \`ownerToken\`. \`claimSessions(claims)\` attaches guest saves to the signed-in account.

Read the state before acting, for example:
query {
  delivery {
    session(id: "${s.id}") {
      tick paused speed
      city { name gridSize rows hiddenTiles kitchens { id name x y } customers { id name x y } depots { id name x y } chargers { id name x y } }
      cars { id name className x y battery maxBattery capacity load freeBoxes state queuedMove queuedMoves lastReject }
      openOrders { id itemName quantity kitchenId kitchenName customerId customerName state assignedCar ticksLeft reward }
      ledger { money reputation deliveries lateDeliveries expiredOrders onTimeStreak tows }
      growth { id tick title description done }
      closures { id reason startTick endTick ticksLeft tiles { x y } }
    }
  }
}

## Game manual
Delivery is a manual fleet game. Assign orders to cars, drive each car to the order's kitchen (pickup is automatic on entry), then to its customer (dropoff is automatic on entry), and deliver before the deadline. Expired orders cost reputation. There is no win condition: keep profitable orders flowing and grow the fleet.

Coordinates and movement: coordinates are (x,y) with row 0 at the TOP and y growing DOWNWARD. NORTH = y-1, SOUTH = y+1, EAST = x+1, WEST = x-1. \`moveCar\` queues a step; \`tick\` applies queued steps in order, one per tick. Up to 64 steps can be queued per car (\`LIMIT_REACHED\` beyond that). A rejected step is spent, not retried, and sets \`lastReject\` on the next tick; use \`clearMoves\` to replan when traffic or closures invalidate a route.

Map glyphs: '.' road, ',' ground (costs one extra battery), '#' water (blocked), 'B' building (blocked), 'D' depot (free charging and parking), 'C' charger (paid charging), 'K' kitchen, 'H' customer waiting, 'h' customer house with no live order, '?' unexplored (blocked), 'X' closed for street works (blocked), digits are cars. Moving into water, a building, an unexplored tile, a closed tile, or off the map is rejected and costs nothing. A car may always leave a closed tile it already occupies. Cars cannot share a road tile; depots hold any number and chargers hold their capacity.

Orders and capacity: an order's quantity is boxes, and \`assignOrder\` admits it only when it fits beside the boxes the car already carries plus the boxes promised to its other assigned-but-unpicked orders; a car may hold several orders at once while their total fits. \`freeBoxes\` is only the currently empty hold, so subtract outstanding assignments before batching. The rejection names the carried, assigned and capacity figures. \`ticksLeft\` is signed: negative means the deadline passed but the tick has not swept the order yet. Assigning refreshes the deadline. Buy a bigger car at a depot with \`buyCar\` when the work outgrows the fleet.

Battery: every step spends the car's \`stepCost\`, plus the city's ground surcharge on ','. Charging on a depot is free; a standalone charger costs \`charger_price_per_tick\` per tick. A car with no battery and no route to a charger is STRANDED; \`towCar\` returns it to the nearest depot for a fee.

Growth and closures: growth stages open districts, kitchens, customers, chargers and depot bays automatically; hidden tiles become passable only when a stage reveals them. Street works ('X' and \`closures\`) are temporary and block entry.

## How to play
1. Keep the clock deterministic: call \`setSpeed(id: "${s.id}", speed: 0${tokenArg})\` if it is running.
2. Read the session (or \`carView\` for a large city) and note the city rows, cars and open orders.
3. Assign a live order to a car that can carry it with \`assignOrder\`.
4. Queue one step at a time with \`moveCar\`, then call \`tick(id: "${s.id}", count: 1${tokenArg})\`; inspect the returned snapshot.
5. If \`lastReject\` is BLOCKED, CLOSED, OCCUPIED, NO_BATTERY or OFF_GRID, replan; the rejected move spent no battery. Repeat until the order is DONE, then take the next one.
6. Use \`carView(id, carId, radius)\` for cheap polling and \`events(id, sinceTick)\` for deltas.

Play steadily and report what you delivered, the final tick, money, reputation and any expired orders. Do not delete the save or guess other sessions' ids.`;
}
