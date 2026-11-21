import { createClient, cacheExchange, fetchExchange, mapExchange, subscriptionExchange, type Client } from '@urql/svelte';
import { createClient as createWsClient, type Client as WsClient } from 'graphql-ws';
import { getAdminKey } from './admin';
import { authHeaders, wsConnectionParams } from '$lib/account/token';

const GRAPHQL_URL =
	typeof window !== 'undefined' && import.meta.env?.PUBLIC_GRAPHQL_URL
		? import.meta.env.PUBLIC_GRAPHQL_URL
		: '/graphql';

const WS_URL =
	typeof window !== 'undefined' && import.meta.env?.PUBLIC_WS_URL
		? import.meta.env.PUBLIC_WS_URL
		: typeof window !== 'undefined'
			? `${window.location.protocol === 'https:' ? 'wss' : 'ws'}://${window.location.host}/graphql`
			: import.meta.env?.DEV
				? 'ws://localhost:8080/graphql'
				: '';

let wsClient: WsClient | null = null;

/** Returns the shared graphql-ws client, creating it lazily. */
export function getWsClient(): WsClient {
	if (!wsClient) {
		wsClient = createWsClient({ url: WS_URL, connectionParams: () => wsConnectionParams() });
	}
	return wsClient;
}

/** Creates the urql client. Writes to built-in maps carry X-Admin-Key read from sessionStorage per request. */
export function makeClient(): Client {
	return createClient({
		url: GRAPHQL_URL,
		// Mutations/queries must always reflect server state; the document cache would hide runs.
		requestPolicy: 'network-only',
		// The Go server only accepts POST; urql defaults to GET for short queries.
		preferGetMethod: false,
		fetchOptions: () => {
			const key = getAdminKey();
			return { headers: { ...authHeaders(), ...(key ? { 'X-Admin-Key': key } : {}) } };
		},
		exchanges: [
			// Documents select under `motorProgramming`; unwrap it so screens
			// keep reading the game-local shape.
			mapExchange({
				onResult(result) {
					if (!result.data) return result;
					const data = result.data as Record<string, unknown>;
					if ('motorProgramming' in data) {
						return { ...result, data: data.motorProgramming as Record<string, unknown> };
					}
					if ('motorProgrammingSessionUpdated' in data) {
						return { ...result, data: { sessionUpdated: data.motorProgrammingSessionUpdated } };
					}
					return result;
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
