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

# GraphQL POST helper: fails on transport errors or a GraphQL "errors" array.
gql_post() {
	label=$1
	body=$2
	out=$(curl -fsS "$BASE_URL/graphql" -H 'content-type: application/json' --data "$body") ||
		fail "$label GraphQL did not answer"
	if echo "$out" | grep -q '"errors"'; then
		fail "$label GraphQL returned errors: $out"
	fi
	printf '%s' "$out"
}

query() {
	game=$1
	body=$2
	out=$(gql_post "$game" "$body")
	echo "$out" | grep -q "\"$game\"" || fail "$game namespace missing: $out"
	echo "smoke: $game ok"
}

# Delivery: the catalogue query must include the shipped default city, then a
# session is created on a second city when one exists, else on the default.
check_delivery() {
	cities=$(gql_post delivery '{"query":"query { delivery { cities { id name status } } }"}')
	echo "$cities" | grep -q '"delivery"' || fail "delivery namespace missing: $cities"
	echo "$cities" | grep -q '"id":"city"' || fail "default city 'city' missing: $cities"
	second=""
	for id in $(printf '%s' "$cities" | grep -o '"id":"[^"]*"' | sed 's/.*"id":"//;s/"$//'); do
		if [ "$id" != "city" ]; then
			second=$id
			break
		fi
	done
	if [ -n "$second" ]; then
		body='{"query":"mutation { delivery { createSession(cityId: \"SECOND_CITY\") { session { id } } } }"}'
		body=$(printf '%s' "$body" | sed "s/SECOND_CITY/$second/")
		echo "smoke: delivery created a session on builtin city $second"
	else
		body='{"query":"mutation { delivery { createSession { session { id } } } }"}'
		echo "smoke: delivery created a session on the default city"
	fi
	created=$(gql_post delivery-session "$body")
	sid=$(printf '%s' "$created" | grep -o '"id":"[^"]*"' | head -n1 | sed 's/.*"id":"//;s/"$//')
	[ -n "$sid" ] || fail "delivery createSession returned no session id: $created"
	read_body='{"query":"query { delivery { session(id: \"SESSION_ID\") { id } } }"}'
	read_body=$(printf '%s' "$read_body" | sed "s/SESSION_ID/$sid/")
	read=$(gql_post delivery-session-read "$read_body")
	echo "$read" | grep -q "\"id\":\"$sid\"" || fail "delivery session $sid did not read back: $read"
	echo "smoke: delivery ok"
}

case " $GAMES " in *" mazes "*) query mazes '{"query":"query { mazes { maps { id } } }"}' ;; esac
case " $GAMES " in *" motor "*) query motorProgramming '{"query":"query { motorProgramming { maps { id } } }"}' ;; esac
case " $GAMES " in *" tesla "*) query tesla '{"query":"query { tesla { maps { mapId } } }"}' ;; esac
case " $GAMES " in *" delivery "*) check_delivery ;; esac
case " $GAMES " in *" music "*) query music '{"query":"query { music { excerpts { id } } }"}' ;; esac

# 3. The SPA shell is served by the same process.
curl -fsS "$BASE_URL/" | grep -qi '<!doctype html' || fail "SPA shell missing at $BASE_URL/"
echo "smoke: SPA ok"

echo "smoke: all checks passed"
