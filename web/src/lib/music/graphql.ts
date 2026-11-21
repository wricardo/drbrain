import { createClient, cacheExchange, fetchExchange, mapExchange, subscriptionExchange, type Client } from '@urql/svelte';
import { createClient as createWsClient, type Client as WsClient } from 'graphql-ws';
import { authHeaders, wsConnectionParams } from '$lib/account/token';
import { unwrap } from '@drbrain/ui/graphql';
import type { SessionSource } from '@drbrain/ui/session';
import {
	CREATE_SESSION_MUTATION,
	EXCERPTS_QUERY,
	EXCERPT_QUERY,
	RESET_ARRANGEMENT_MUTATION,
	SESSION_QUERY,
	SESSION_UPDATED_SUBSCRIPTION,
	SWITCH_MEASURES_MUTATION,
	TRANSFORM_MEASURE_MUTATION
} from './queries';
import type { Difficulty, Excerpt, Session, SessionUpdate } from './types';

const GRAPHQL_URL =
	typeof window !== 'undefined' && import.meta.env?.PUBLIC_GRAPHQL_URL
		? import.meta.env.PUBLIC_GRAPHQL_URL
		: '/graphql';

const WS_URL =
	typeof window !== 'undefined' && import.meta.env?.PUBLIC_WS_URL
		? import.meta.env.PUBLIC_WS_URL
		: typeof window !== 'undefined'
			? `${window.location.protocol === 'https:' ? 'wss' : 'ws'}://${window.location.host}/graphql`
			: '';

let wsClient: WsClient | null = null;

/** Returns the shared graphql-ws client, creating it lazily. */
export function getWsClient(): WsClient {
	if (!wsClient) {
		wsClient = createWsClient({ url: WS_URL, connectionParams: () => wsConnectionParams() });
	}
	return wsClient;
}

/**
 * Documents select under the `music` namespace; unwrap it so every screen
 * keeps reading the game-local shape. Subscriptions are flat roots
 * (`musicSessionUpdated`) and are mapped back to the shared `sessionUpdated`.
 */
export function unwrapMusic(data: Record<string, unknown>): Record<string, unknown> {
	if ('music' in data) return data.music as Record<string, unknown>;
	if ('musicSessionUpdated' in data) return { sessionUpdated: data.musicSessionUpdated };
	return data;
}

/** Creates the urql client for the Music Region. */
export function makeClient(): Client {
	return createClient({
		url: GRAPHQL_URL,
		fetchOptions: () => ({ headers: authHeaders() }),
		// Snapshots must always reflect server state; the document cache would hide arrangements.
		requestPolicy: 'network-only',
		// The Go server only accepts POST; urql defaults to GET for short queries.
		preferGetMethod: false,
		exchanges: [
			mapExchange({
				onResult(result) {
					if (!result.data) return result;
					return { ...result, data: unwrapMusic(result.data as Record<string, unknown>) };
				}
			}),
			cacheExchange,
			fetchExchange,
			subscriptionExchange({
				forwardSubscription(request) {
					const input = { ...request, query: request.query ?? '' };
					return {
						subscribe(sink) {
							const unsubscribe = getWsClient().subscribe(input, sink);
							return { unsubscribe };
						}
					};
				}
			})
		]
	});
}

/** The public catalogue. */
export async function fetchExcerpts(client: Client): Promise<Excerpt[]> {
	const data = await unwrap<{ excerpts: Excerpt[] }>(client.query(EXCERPTS_QUERY, {}));
	return data.excerpts;
}

/** One excerpt; null when it disappeared since the catalogue was read. */
export async function fetchExcerpt(client: Client, id: string): Promise<Excerpt | null> {
	const data = await unwrap<{ excerpt: Excerpt | null }>(client.query(EXCERPT_QUERY, { id }, { requestPolicy: 'network-only' }));
	return data.excerpt;
}

/** Creates (or resumes, server-side policy permitting) a puzzle session. */
export async function createSession(
	client: Client,
	excerptID: string,
	difficulty: Difficulty,
	displayName?: string,
	seed?: number
): Promise<Session> {
	const data = await unwrap<{ createSession: Session }>(
		client.mutation(CREATE_SESSION_MUTATION, { excerptID, difficulty, displayName, seed })
	);
	return data.createSession;
}

export async function switchMeasures(client: Client, sessionID: string, a: number, b: number): Promise<Session> {
	const data = await unwrap<{ switchMeasures: Session }>(client.mutation(SWITCH_MEASURES_MUTATION, { sessionID, a, b }));
	return data.switchMeasures;
}

export async function transformMeasure(
	client: Client,
	sessionID: string,
	position: number,
	flipH: boolean,
	flipV: boolean
): Promise<Session> {
	const data = await unwrap<{ transformMeasure: Session }>(
		client.mutation(TRANSFORM_MEASURE_MUTATION, { sessionID, position, flipH, flipV })
	);
	return data.transformMeasure;
}

export async function resetArrangement(client: Client, sessionID: string): Promise<Session> {
	const data = await unwrap<{ resetArrangement: Session }>(client.mutation(RESET_ARRANGEMENT_MUTATION, { sessionID }));
	return data.resetArrangement;
}

/** Real session transport: urql for the snapshot query, graphql-ws for the subscription. */
export function createSessionSource(client: Client): SessionSource<Session, SessionUpdate> {
	return {
		async fetch(id) {
			const res = await client.query<{ session: Session | null }>(SESSION_QUERY, { id }, { requestPolicy: 'network-only' }).toPromise();
			if (res.error) throw res.error;
			return res.data?.session ?? null;
		},
		subscribe(id, sink) {
			return getWsClient().subscribe<{ musicSessionUpdated: SessionUpdate }>(
				{ query: SESSION_UPDATED_SUBSCRIPTION, variables: { sessionID: id } },
				{
					next(result) {
						if (result.errors?.length) sink.error(result.errors);
						else if (result.data?.musicSessionUpdated) sink.next(result.data.musicSessionUpdated);
					},
					error: (err) => sink.error(err),
					complete: () => sink.complete()
				}
			);
		},
		onConnection(cb) {
			const ws = getWsClient();
			let everConnected = false;
			const offs = [
				ws.on('connecting', () => cb(everConnected ? 'reconnecting' : 'connecting')),
				ws.on('connected', () => {
					everConnected = true;
					cb('live');
				}),
				ws.on('closed', () => cb(everConnected ? 'reconnecting' : 'offline'))
			];
			return () => offs.forEach((off) => off());
		}
	};
}
