#!/bin/sh
# Smoke test one Dr Brain deployment: /healthz must list every enabled game as
# ok, and each game's GraphQL endpoint must answer a real query.
#
# Usage: BASE_URL=http://127.0.0.1:8080 GAMES="mazes motor tesla delivery" ./deploy/smoke.sh
set -eu

BASE_URL="${BASE_URL:-http://127.0.0.1:8080}"
GAMES="${GAMES:-mazes motor tesla delivery}"

fail() {
	echo "smoke: $*" >&2
	exit 1
}

# 1. /healthz: status ok and one healthy entry per enabled game.
health=$(curl -fsS "$BASE_URL/healthz") || fail "healthz did not answer at $BASE_URL/healthz"
echo "smoke: healthz $health"
echo "$health" | grep -q '"status":"ok"' || fail "healthz is not ok"
for game in $GAMES; do
	echo "$health" | grep -q "\"$game\":\"ok\"" || fail "healthz does not report $game ok"
done

# 2. One query per game that exercises its schema.
query() {
	game=$1
	body=$2
	out=$(curl -fsS "$BASE_URL/$game/graphql" -H 'content-type: application/json' --data "$body") ||
		fail "$game GraphQL did not answer"
	echo "$out" | grep -q '"data"' || fail "$game GraphQL returned no data: $out"
	echo "smoke: $game ok"
}

case " $GAMES " in *" mazes "*) query mazes '{"query":"query { maps { id } }"}' ;; esac
case " $GAMES " in *" motor "*) query motor '{"query":"query { maps { id } }"}' ;; esac
case " $GAMES " in *" tesla "*) query tesla '{"query":"query { maps { mapId } }"}' ;; esac
case " $GAMES " in *" delivery "*) query delivery '{"query":"query { city { name gridSize } }"}' ;; esac

# 3. The SPA shell is served by the same process.
curl -fsS "$BASE_URL/" | grep -qi '<!doctype html' || fail "SPA shell missing at $BASE_URL/"
echo "smoke: SPA ok"

echo "smoke: all checks passed"
