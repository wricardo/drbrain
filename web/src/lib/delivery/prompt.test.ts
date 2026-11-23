import { describe, expect, it } from 'vitest';
import { buildSessionPrompt, type PromptOptions, type PromptSession } from './prompt';

/** A full snapshot: the prompt must use only the id and never echo any of this state. */
const snapshot = {
	id: 'abc123',
	tick: 42,
	speed: 0,
	paused: true,
	city: {
		name: 'DrBrain City',
		gridSize: 5,
		rows: ['.....', '..K..', '.....', '.D.C.', '..H..'],
		hiddenTiles: 0,
		kitchens: [{ id: 'k1', name: 'Pizza Palace', x: 2, y: 1 }],
		customers: [{ id: 'c1', name: 'Rosa', x: 2, y: 4 }],
		depots: [{ id: 'd_main', name: 'Depot', x: 1, y: 3 }],
		chargers: [{ id: 'ch1', name: 'Charger', x: 3, y: 3 }]
	},
	cars: [{ id: 'car1', name: 'Scooter One', className: 'scooter', x: 1, y: 3, battery: 20, maxBattery: 24 }],
	orders: [{ id: 'o1', itemName: 'Pizza', quantity: 2, state: 'ASSIGNED', ticksLeft: -3, reward: 50 }],
	ledger: { money: 120, reputation: 3.5, deliveries: 4 },
	growth: [{ id: 'g1', tick: 180, title: 'Corner Charger', done: false }]
};

const prompt = (over: Partial<PromptSession> = {}, opts: PromptOptions = { ownerToken: 'tok-secret', canPlay: true }) =>
	buildSessionPrompt('http://h:1', { ...snapshot, ...over }, opts);

describe('buildSessionPrompt', () => {
	it('carries the ids, endpoints, credential, schema pointers and main operations', () => {
		const p = prompt();

		expect(p).toContain('Session ID: abc123');
		expect(p).toContain('http://h:1/graphql');
		expect(p).toContain('http://h:1/graphql/playground');
		expect(p).toContain('http://h:1/delivery/llms.txt');
		expect(p).toContain('http://h:1/delivery?city=abc123');
		expect(p).toContain('tok-secret');
		expect(p).toContain('__schema');
		expect(p).toContain('session(id: ID!): Delivery_Snapshot!');
		expect(p).toContain('carView');
		expect(p).toContain('events(id: ID!, sinceTick');
		expect(p).toContain('canPlaySession');
		expect(p).toContain('assignOrder');
		expect(p).toContain('unassignOrder');
		expect(p).toContain('moveCar');
		expect(p).toContain('clearMoves');
		expect(p).toContain('`tick(id, count: Int = 1');
		expect(p).toContain('setSpeed');
		expect(p).toContain('towCar');
		expect(p).toContain('buyCar');
		expect(p).toContain('saveSession');
		expect(p).toContain('deliverySession');
	});

	it('includes a game manual instead of the current state', () => {
		const p = prompt();

		expect(p).toContain('Game manual');
		expect(p).toContain('pickup is automatic');
		expect(p).toContain('NORTH = y-1');
		expect(p).toContain('freeBoxes');
		expect(p).toContain('STRANDED');
		expect(p).toContain('How to play');

		expect(p).not.toContain('at tick 42');
		expect(p).not.toContain('Money: $120');
		expect(p).not.toContain('DrBrain City');
		expect(p).not.toContain('Pizza Palace at (2,1)');
		expect(p).not.toContain('Rosa at (2,4)');
		expect(p).not.toContain('Scooter One');
		expect(p).not.toContain('o1:');
		expect(p).not.toContain('Next growth: Corner Charger');
		expect(p).not.toContain('y 0  .....');
	});

	it('omits the credential and marks a spectator prompt read-only', () => {
		const p = prompt({}, { ownerToken: null, canPlay: false });

		expect(p).not.toContain('tok-secret');
		expect(p).not.toContain('Owner token (secret');
		expect(p).toContain('Read-only view');
		expect(p).toContain('mutations will be FORBIDDEN');
		expect(p).not.toContain('Authorization: Bearer <token>');
	});

	it('asks for a bearer token instead of printing one for an account-owned city', () => {
		const p = prompt({}, { ownerToken: null, canPlay: true });

		expect(p).toContain('Authorization: Bearer <token>');
		expect(p).toContain('Do not ask for an owner token');
		expect(p).not.toContain('Owner token (secret');
	});

	it('is independent of the state it is handed', () => {
		const quiet = prompt({ tick: 0, ledger: { money: 0, reputation: 5, deliveries: 0 } } as never);
		const busy = prompt({ tick: 9000, ledger: { money: 999999, reputation: 0, deliveries: 300 } } as never);

		expect(quiet).toBe(busy);
	});
});
