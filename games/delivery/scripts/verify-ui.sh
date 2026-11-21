#!/usr/bin/env bash
# Build the monorepo server, start it with only Delivery on a free port, run the
# headless UI smoke test against it, then shut it down. The session save
# directory is temporary so a verification run never touches real saves.
# Needs web/build (cd web && npm run build) for the pages to be served.
set -euo pipefail

cd "$(dirname "$0")/../../.."
ROOT="$PWD"

PORT="${PORT:-8091}"
BIN="tmp/drbrain-ui"
OUT="${OUT:-/tmp/drbrain-ui.png}"
SPECTATOR_OUT="${SPECTATOR_OUT:-${OUT%.png}-spectator.png}"

mkdir -p tmp
go build -o "$BIN" ./cmd/drbrain

TMP_SESSIONS="$(mktemp -d)"
cleanup() {
	if [ -n "${SRV_PID:-}" ] && kill -0 "$SRV_PID" 2>/dev/null; then
		kill -TERM "$SRV_PID" 2>/dev/null || true
		wait "$SRV_PID" 2>/dev/null || true
	fi
	rm -rf "$TMP_SESSIONS"
}
trap cleanup EXIT

DRBRAIN_ADDR="localhost:$PORT" \
	DRBRAIN_GAMES=delivery \
	DRBRAIN_STATIC_DIR=web/build \
	DELIVERY_SESSIONS_DIR="$TMP_SESSIONS" \
	DELIVERY_CITY=games/delivery/maps/city.json \
	"$BIN" serve >/tmp/drbrain-verify.log 2>&1 &
SRV_PID=$!

health() {
	curl -sf -X POST "localhost:$PORT/graphql" \
		-H "Content-Type: application/json" \
		-d '{"query":"{ delivery { health { status } } }"}' >/dev/null 2>&1
}

for _ in $(seq 1 60); do
	if health; then break; fi
	sleep 0.1
done

if ! health; then
	echo "error: server did not start; see /tmp/drbrain-verify.log" >&2
	exit 1
fi

cd "$ROOT/games/delivery"
node scripts/ui-smoke.mjs "http://localhost:$PORT" "$OUT" "$SPECTATOR_OUT"
