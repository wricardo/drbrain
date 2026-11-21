#!/bin/sh
# Smoke test one Dr Brain deployment: /healthz must list every enabled game as
# ok, and the one unified GraphQL endpoint must answer a real namespaced query
# per game. There is no per-game /<game>/graphql route.
#
# Usage: BASE_URL=http://127.0.0.1:8080 GAMES="mazes motor tesla delivery music" ./deploy/smoke.sh
set -eu

BASE_URL="${BASE_URL:-http://127.0.0.1:8080}"
GAMES="${GAMES:-mazes motor tesla delivery music}"

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

# 2. One namespaced query per game against the unified /graphql endpoint.
query() {
	game=$1
	body=$2
	out=$(curl -fsS "$BASE_URL/graphql" -H 'content-type: application/json' --data "$body") ||
		fail "$game GraphQL did not answer"
	if echo "$out" | grep -q '"errors"'; then
		fail "$game GraphQL returned errors: $out"
	fi
	echo "$out" | grep -q "\"$game\"" || fail "$game namespace missing: $out"
	echo "smoke: $game ok"
}

case " $GAMES " in *" mazes "*) query mazes '{"query":"query { mazes { maps { id } } }"}' ;; esac
case " $GAMES " in *" motor "*) query motorProgramming '{"query":"query { motorProgramming { maps { id } } }"}' ;; esac
case " $GAMES " in *" tesla "*) query tesla '{"query":"query { tesla { maps { mapId } } }"}' ;; esac
case " $GAMES " in *" delivery "*) query delivery '{"query":"query { delivery { city { name gridSize } } }"}' ;; esac
case " $GAMES " in *" music "*) query music '{"query":"query { music { excerpts { id } } }"}' ;; esac

# 3. The SPA shell is served by the same process.
curl -fsS "$BASE_URL/" | grep -qi '<!doctype html' || fail "SPA shell missing at $BASE_URL/"
echo "smoke: SPA ok"

echo "smoke: all checks passed"
