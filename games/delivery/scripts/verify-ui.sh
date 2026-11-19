#!/usr/bin/env bash
# Start a throwaway server on a free port, run the headless UI smoke test
# against it, then shut it down. The session save directory is temporary so a
# verification run never touches real saves.
set -euo pipefail

cd "$(dirname "$0")/.."

PORT="${PORT:-8091}"
BIN="./bin/drbrain-server"
OUT="${OUT:-/tmp/drbrain-ui.png}"

if [ ! -x "$BIN" ]; then
	echo "error: $BIN not built; run make build or make verify-ui" >&2
	exit 1
fi

TMP_SESSIONS="$(mktemp -d)"
cleanup() {
	if [ -n "${SRV_PID:-}" ] && kill -0 "$SRV_PID" 2>/dev/null; then
		kill -TERM "$SRV_PID" 2>/dev/null || true
		wait "$SRV_PID" 2>/dev/null || true
	fi
	rm -rf "$TMP_SESSIONS"
}
trap cleanup EXIT

"$BIN" -addr "localhost:$PORT" -sessions-dir "$TMP_SESSIONS" >/tmp/drbrain-verify.log 2>&1 &
SRV_PID=$!

for _ in $(seq 1 60); do
	if curl -sf -X POST "localhost:$PORT/graphql" \
		-H "Content-Type: application/json" \
		-d '{"query":"{ health { status } }"}' >/dev/null 2>&1; then break; fi
	sleep 0.1
done

if ! curl -sf -X POST "localhost:$PORT/graphql" \
	-H "Content-Type: application/json" \
	-d '{"query":"{ health { status } }"}' >/dev/null 2>&1; then
	echo "error: server did not start; see /tmp/drbrain-verify.log" >&2
	exit 1
fi

node scripts/ui-smoke.mjs "http://localhost:$PORT" "$OUT"
