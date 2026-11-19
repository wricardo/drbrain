import { createClient, cacheExchange, fetchExchange, subscriptionExchange, type Client } from '@urql/svelte';
import { createClient as createWsClient, type Client as WsClient } from 'graphql-ws';

export interface GraphQLUrls {
	http: string;
	ws: string;
}

/**
 * URLs for one game. The route prefix owns the API path, so every game shares
 * the client but not its endpoint: /mazes/graphql, /motor/graphql, ...
 * PUBLIC_GRAPHQL_URL / PUBLIC_WS_URL override both for local dev.
 */
export function graphqlUrls(prefix: string): GraphQLUrls {
	const http = import.meta.env?.PUBLIC_GRAPHQL_URL || `${prefix}/graphql`;
	if (import.meta.env?.PUBLIC_WS_URL) {
		return { http, ws: import.meta.env.PUBLIC_WS_URL };
	}
	if (typeof window !== 'undefined') {
		const scheme = window.location.protocol === 'https:' ? 'wss' : 'ws';
		return { http, ws: `${scheme}://${window.location.host}${prefix}/graphql` };
	}
	return { http, ws: `ws://localhost:8080${prefix}/graphql` };
}

export interface GraphQLOptions {
	/** Extra headers per request (e.g. X-Admin-Key from sessionStorage). */
	headers?: () => Record<string, string>;
}

export interface GraphQLClient {
	client: Client;
	wsClient: () => WsClient;
}

/** Builds the urql + graphql-ws pair for a game prefix. */
export function createGraphQL(prefix: string, options: GraphQLOptions = {}): GraphQLClient {
	const urls = graphqlUrls(prefix);
	let ws: WsClient | null = null;
	const wsClient = () => {
		if (!ws) ws = createWsClient({ url: urls.ws });
		return ws;
	};
	const client = createClient({
		url: urls.http,
		requestPolicy: 'network-only',
		preferGetMethod: false,
		fetchOptions: () => {
			const extra = options.headers?.() ?? {};
			return Object.keys(extra).length ? { headers: extra } : {};
		},
		exchanges: [
			cacheExchange,
			fetchExchange,
			subscriptionExchange({
				forwardSubscription(request) {
					const input = { ...request, query: request.query ?? '' };
					return {
						subscribe(sink) {
							const unsubscribe = wsClient().subscribe(input, sink);
							return { unsubscribe };
						}
					};
				}
			})
		]
	});
	return { client, wsClient };
}
