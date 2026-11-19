// Subscribing to a session over Server-Sent Events.
//
// Why SSE and hand-rolled rather than a GraphQL client: the app has no runtime
// dependencies (see package.json — everything is a devDependency and the bundle
// is plain Svelte), and `fetch` + ReadableStream is enough to read gqlgen's SSE
// frames. Adding urql or graphql-ws to watch one field would outweigh the whole
// frontend.
//
// Wire format is gqlgen's transport.SSE: a POST whose response is
// `text/event-stream`, one frame per emitted value:
//
//	event: next
//	data: {"data":{"session":{...}}}
//
// A `: ping` comment arrives every KeepAlivePingInterval (15s) so an idle
// stream — a paused city can legitimately sit for minutes — is not dropped by
// an intermediary.

import type { Snapshot } from './api';

/** Frames the server emits, mapped to the state a caller sees. */
export interface SessionSubscriptionHandlers {
	/** A new snapshot arrived. May be called more than once for the same tick. */
	next(snapshot: Snapshot): void;
	/**
	 * The stream ended or failed. The caller decides whether to reconnect: this
	 * module never retries on its own, so a component can stop on unmount
	 * without racing a background timer.
	 */
	closed(reason: string): void;
}

/** Query text for the subscription. Mirrors SNAPSHOT_FIELDS in api.ts. */
const SUBSCRIPTION = (fields: string) => `subscription ($id: ID!) { session(id: $id) { ${fields} } }`;

/**
 * subscribeToSession opens an SSE stream for one session.
 *
 * Returns an abort function. Calling it stops the stream; the `closed` handler
 * is NOT invoked for an abort, because that is an intentional teardown rather
 * than a stream failure a caller should react to.
 */
export function subscribeToSession(
	id: string,
	fields: string,
	handlers: SessionSubscriptionHandlers
): () => void {
	const ac = new AbortController();
	let aborted = false;

	void (async () => {
		let res: Response;
		try {
			res = await fetch('/graphql', {
				method: 'POST',
				headers: { 'Content-Type': 'application/json', Accept: 'text/event-stream' },
				body: JSON.stringify({ query: SUBSCRIPTION(fields), variables: { id } }),
				signal: ac.signal
			});
		} catch (e) {
			if (!aborted) handlers.closed(`request failed: ${String(e)}`);
			return;
		}

		if (!res.ok || !res.body) {
			// A GraphQL error (bad id, unknown field) arrives as a normal JSON
			// response rather than a stream, so surface it as prose.
			let detail = `HTTP ${res.status}`;
			try {
				const text = await res.text();
				const parsed = JSON.parse(text) as { errors?: { message: string }[] };
				if (parsed.errors?.length) detail = parsed.errors[0].message;
			} catch {
				/* keep the status line */
			}
			if (!aborted) handlers.closed(detail);
			return;
		}

		const reader = res.body.getReader();
		const decoder = new TextDecoder();
		let buffer = '';

		try {
			for (;;) {
				const { done, value } = await reader.read();
				if (done) break;
				buffer += decoder.decode(value, { stream: true });

				// Frames are separated by a blank line. Anything before the last
				// separator is complete; the remainder stays buffered because a
				// chunk boundary can split a frame.
				let sep: number;
				while ((sep = buffer.indexOf('\n\n')) >= 0) {
					const frame = buffer.slice(0, sep);
					buffer = buffer.slice(sep + 2);
					handleFrame(frame, handlers);
				}
			}
			if (!aborted) handlers.closed('stream ended');
		} catch (e) {
			if (!aborted) handlers.closed(`stream error: ${String(e)}`);
		}
	})();

	return () => {
		aborted = true;
		ac.abort();
	};
}

/**
 * handleFrame parses one SSE frame.
 *
 * `:` lines are comments (the keep-alive ping) and are ignored. A frame with no
 * data is ignored too: the transport sends an empty comment first to open the
 * stream, and reacting to that would fire a spurious "closed".
 */
function handleFrame(frame: string, handlers: SessionSubscriptionHandlers): void {
	let event = 'next';
	const data: string[] = [];
	for (const line of frame.split('\n')) {
		if (line.startsWith(':')) continue;
		if (line.startsWith('event:')) event = line.slice(6).trim();
		else if (line.startsWith('data:')) data.push(line.slice(5).trimStart());
	}
	if (event === 'complete') {
		handlers.closed('subscription completed');
		return;
	}
	if (data.length === 0) return;
	try {
		const parsed = JSON.parse(data.join('\n')) as {
			data?: { session?: Snapshot };
			errors?: { message: string }[];
		};
		if (parsed.errors?.length) {
			handlers.closed(parsed.errors[0].message);
			return;
		}
		const snapshot = parsed.data?.session;
		if (snapshot) handlers.next(snapshot);
	} catch {
		// A malformed frame is not worth tearing the stream down over.
	}
}
