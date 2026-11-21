import type { Client } from '@urql/svelte';
import { getWsClient } from '../graphql';
import { SESSION_QUERY, SESSION_UPDATED_SUBSCRIPTION } from '../queries';
import type { ConnectionState, Session, SessionUpdate } from '../types';
import type { SessionSource } from '@drbrain/ui/session';

/**
 * Real session transport: urql for the snapshot query, graphql-ws for the subscription.
 * The owner token is what makes the server include the program; without it the session is a
 * spectator view and its `program` is null.
 */
export function createSessionSource(client: Client, ownerToken?: string | null): SessionSource<Session, SessionUpdate> {
	const token = ownerToken ?? null;
	return {
		async fetch(id) {
			const res = await client
				.query<{ session: Session | null }>(SESSION_QUERY, { id, ownerToken: token }, { requestPolicy: 'network-only' })
				.toPromise();
			if (res.error) throw res.error;
			return res.data?.session ?? null;
		},
		subscribe(id, sink) {
			return getWsClient().subscribe<{ motorProgrammingSessionUpdated: SessionUpdate }>(
				{ query: SESSION_UPDATED_SUBSCRIPTION, variables: { sessionID: id, ownerToken: token } },
				{
					next(result) {
						if (result.errors?.length) sink.error(result.errors);
						else if (result.data?.motorProgrammingSessionUpdated) sink.next(result.data.motorProgrammingSessionUpdated);
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

export type { ConnectionState };
