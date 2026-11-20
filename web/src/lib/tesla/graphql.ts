import { createClient, cacheExchange, fetchExchange, mapExchange, type Client } from '@urql/svelte';
import { createClient as createWsClient, type Client as WsClient } from 'graphql-ws';
import { subscriptionExchange } from '@urql/svelte';

const GRAPHQL_URL = (typeof window !== 'undefined' && import.meta.env?.PUBLIC_GRAPHQL_URL)
  ? import.meta.env.PUBLIC_GRAPHQL_URL
  : '/graphql';

const WS_URL = (typeof window !== 'undefined' && import.meta.env?.PUBLIC_WS_URL)
  ? import.meta.env.PUBLIC_WS_URL
  : (typeof window !== 'undefined'
    ? `${window.location.protocol === 'https:' ? 'wss' : 'ws'}://${window.location.host}/graphql`
    : (import.meta.env.DEV ? 'ws://localhost:8080/graphql' : ''));

let wsClient: WsClient | null = null;

function getWsClient(): WsClient {
  if (!wsClient) {
    wsClient = createWsClient({ url: WS_URL });
  }
  return wsClient;
}

export function makeClient(): Client {
  return createClient({
    url: GRAPHQL_URL,
    exchanges: [
      // Documents select under `tesla`; unwrap it so screens keep reading the
      // game-local shape. Subscriptions have flat prefixed root fields.
      mapExchange({
        onResult(result) {
          if (!result.data) return result;
          const data = result.data as Record<string, unknown>;
          if ('tesla' in data) {
            return { ...result, data: data.tesla as Record<string, unknown> };
          }
          if ('teslaSessionUpdated' in data) {
            return { ...result, data: { sessionUpdated: data.teslaSessionUpdated } };
          }
          if ('teslaLobbyUpdated' in data) {
            return { ...result, data: { lobbyUpdated: data.teslaLobbyUpdated } };
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
