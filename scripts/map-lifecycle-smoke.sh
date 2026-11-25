#!/bin/sh
# Cross-game map/city lifecycle smoke test against one unified GraphQL endpoint.
#
# Creates two throwaway accounts, then exercises the community-map lifecycle for
# Tesla, Mazes and Delivery through the public API only:
#   guest denied -> owner creates a DRAFT -> other player cannot see/edit/delete
#   -> owner publishes -> other player cannot publish -> owner starts a session
#   -> owner deletes the session, map/city.
#
# Every run uses unique ids, so it can be rerun against the same database.
# Cleanup is GraphQL-only (no SQL): the trap removes the sessions and maps/cities
# even when a check fails.
#
# Usage:
#   BASE_URL=http://127.0.0.1:8184 ./scripts/map-lifecycle-smoke.sh
#   GRAPHQL=http://127.0.0.1:8184/graphql ./scripts/map-lifecycle-smoke.sh
set -eu

ROOT=$(cd "$(dirname "$0")/.." && pwd)
BASE_URL="${BASE_URL:-http://127.0.0.1:8080}"
GRAPHQL="${GRAPHQL:-$BASE_URL/graphql}"
TESLA_FIXTURE="${TESLA_FIXTURE:-$ROOT/games/tesla/maps/classic.json}"
MAZES_FIXTURE="${MAZES_FIXTURE:-$ROOT/games/mazes/maps/first_steps.json}"
DELIVERY_FIXTURE="${DELIVERY_FIXTURE:-$ROOT/games/delivery/maps/saopaulo.json}"

for f in "$TESLA_FIXTURE" "$MAZES_FIXTURE" "$DELIVERY_FIXTURE"; do
	[ -f "$f" ] || { echo "smoke: missing fixture $f" >&2; exit 1; }
done
command -v curl >/dev/null || { echo "smoke: curl is required" >&2; exit 1; }
command -v jq >/dev/null || { echo "smoke: jq is required" >&2; exit 1; }

suffix="$(date +%s)$(printf '%03d' $((RANDOM % 1000)))"
tesla_id="lm_t_${suffix}"
mazes_id="lm_m_${suffix}"
city_id="lm_c_${suffix}"
account_a="lma_${suffix}"
account_b="lmb_${suffix}"
password="tangerine-ladder-42"

TOKEN_A=""
TOKEN_B=""
TESLA_SESSION=""
MAZES_SESSION=""
MAZES_OWNER=""
CITY_SESSION=""
CITY_OWNER=""
CREATED_TESLA=""
CREATED_MAZES=""
CREATED_CITY=""

fail() {
	echo "smoke: $*" >&2
	exit 1
}

show() {
	echo "smoke: $1: $2" >&2
}

# gql TOKEN QUERY VARIABLES_JSON
gql() {
	vars=${3:-'{}'}
	body=$(jq -n --arg q "$2" --argjson v "$vars" '{query:$q,variables:$v}')
	if [ -n "$1" ]; then
		curl -sS "$GRAPHQL" -H 'content-type: application/json' -H "Authorization: Bearer $1" --data "$body"
	else
		curl -sS "$GRAPHQL" -H 'content-type: application/json' --data "$body"
	fi
}

check_ok() {
	label=$1
	resp=$2
	if ! printf '%s' "$resp" | jq -e '((.errors // []) | length) == 0' >/dev/null 2>&1; then
		show "$label" "$resp"
		fail "$label failed"
	fi
}

check_code() {
	label=$1
	resp=$2
	want=$3
	got=$(printf '%s' "$resp" | jq -r '(.errors // [])[0].extensions.code // ""')
	if [ "$got" != "$want" ]; then
		show "$label" "$resp"
		fail "$label returned code ${got:-none}, want $want"
	fi
}

check_code_any() {
	label=$1
	resp=$2
	want=$3
	got=$(printf '%s' "$resp" | jq -r '(.errors // [])[0].extensions.code // ""')
	for w in $want; do
		[ "$got" = "$w" ] && return 0
	done
	show "$label" "$resp"
	fail "$label returned code ${got:-none}, want one of: $want"
}

assert_jq() {
	label=$1
	resp=$2
	shift 2
	if ! printf '%s' "$resp" | jq -e "$@" >/dev/null 2>&1; then
		show "$label" "$resp"
		fail "$label assertion failed"
	fi
}

cleanup() {
	# Best effort; never mask the original failure.
	[ -n "$TESLA_SESSION" ] && gql "$TOKEN_A" \
		'mutation($id:ID!){ tesla { deleteSession(id:$id){ message } } }' \
		"$(jq -n --arg id "$TESLA_SESSION" '{id:$id}')" >/dev/null 2>&1 || true
	[ -n "$MAZES_SESSION" ] && gql "$TOKEN_A" \
		'mutation($id:ID!,$t:String!){ mazes { deleteSession(id:$id,ownerToken:$t) } }' \
		"$(jq -n --arg id "$MAZES_SESSION" --arg t "$MAZES_OWNER" '{id:$id,t:$t}')" >/dev/null 2>&1 || true
	[ -n "$CITY_SESSION" ] && gql "$TOKEN_A" \
		'mutation($id:ID!,$t:String!){ deliveryDeleteSession(id:$id,ownerToken:$t) }' \
		"$(jq -n --arg id "$CITY_SESSION" --arg t "$CITY_OWNER" '{id:$id,t:$t}')" >/dev/null 2>&1 || true
	[ -n "$CREATED_TESLA" ] && gql "$TOKEN_A" \
		'mutation($n:String!){ tesla { deleteMap(name:$n){ message } } }' \
		"$(jq -n --arg n "$TESLA_ID" '{n:$n}')" >/dev/null 2>&1 || true
	[ -n "$CREATED_MAZES" ] && gql "$TOKEN_A" \
		'mutation($id:ID!){ mazes { deleteMap(id:$id) } }' \
		"$(jq -n --arg id "$MAZES_ID" '{id:$id}')" >/dev/null 2>&1 || true
	[ -n "$CREATED_CITY" ] && gql "$TOKEN_A" \
		'mutation($id:ID!){ deliveryDeleteCity(id:$id) }' \
		"$(jq -n --arg id "$CITY_ID" '{id:$id}')" >/dev/null 2>&1 || true
}
TESLA_ID=$tesla_id
MAZES_ID=$mazes_id
CITY_ID=$city_id
trap cleanup EXIT INT TERM

echo "smoke: endpoint $GRAPHQL"
echo "smoke: ids tesla=$tesla_id mazes=$mazes_id delivery=$city_id"

# ---- accounts -------------------------------------------------------------

reg() {
	handle=$1
	resp=$(gql "" 'mutation($h:String!,$p:String!){ account { registerWithPassword(handle:$h,password:$p){ token user { handle } } } }' \
		"$(jq -n --arg h "$handle" --arg p "$password" '{h:$h,p:$p}')")
	check_ok "register $handle" "$resp"
	printf '%s' "$resp" | jq -r '.data.account.registerWithPassword.token'
}
TOKEN_A=$(reg "$account_a")
TOKEN_B=$(reg "$account_b")
[ -n "$TOKEN_A" ] && [ "$TOKEN_A" != "null" ] || fail "account A has no token"
[ -n "$TOKEN_B" ] && [ "$TOKEN_B" != "null" ] || fail "account B has no token"
echo "smoke: registered $account_a and $account_b"

# ---- Tesla ----------------------------------------------------------------

TESLA_MAP=$(jq --arg name "Smoke Tesla $suffix" '{
	name:$name, description:.description,
	gridSize:.grid_size, maxBattery:.max_battery, startingBattery:.starting_battery,
	layout:.layout,
	legend:(.legend | to_entries | map({key:.key, value:.value})),
	cellConfigs:[]
}' "$TESLA_FIXTURE")
tesla_vars=$(jq -n --arg n "$tesla_id" --argjson m "$TESLA_MAP" '{n:$n,m:$m}')

resp=$(gql "" 'mutation($n:String!,$m:Tesla_GameMapInput!){ tesla { createMap(name:$n,map:$m){ name status } } }' "$tesla_vars")
check_code "tesla guest create" "$resp" UNAUTHENTICATED

resp=$(gql "$TOKEN_A" 'mutation($n:String!,$m:Tesla_GameMapInput!){ tesla { createMap(name:$n,map:$m){ name status ownedByMe } } }' "$tesla_vars")
check_ok "tesla createMap" "$resp"
CREATED_TESLA=1
assert_jq "tesla createMap draft" "$resp" '.data.tesla.createMap.status=="DRAFT" and .data.tesla.createMap.ownedByMe==true'

resp=$(gql "$TOKEN_A" 'query { tesla { maps { mapId status ownedByMe } } }')
check_ok "tesla A maps" "$resp"
assert_jq "tesla A sees own draft" "$resp" --arg id "$tesla_id" '.data.tesla.maps[] | select(.mapId==$id) | .status=="DRAFT" and .ownedByMe==true'

resp=$(gql "$TOKEN_B" 'query { tesla { maps { mapId status ownedByMe } } }')
check_ok "tesla B maps" "$resp"
assert_jq "tesla B cannot see A draft" "$resp" --arg id "$tesla_id" '[.data.tesla.maps[] | select(.mapId==$id)] | length==0'

resp=$(gql "$TOKEN_B" 'mutation($n:String!){ tesla { updateMap(name:$n,patch:{description:"hijacked"}){ name } } }' "$(jq -n --arg n "$tesla_id" '{n:$n}')")
check_code_any "tesla B update" "$resp" "FORBIDDEN NOT_FOUND"
resp=$(gql "$TOKEN_B" 'mutation($n:String!){ tesla { deleteMap(name:$n){ message } } }' "$(jq -n --arg n "$tesla_id" '{n:$n}')")
check_code_any "tesla B delete" "$resp" "FORBIDDEN NOT_FOUND"

resp=$(gql "$TOKEN_A" 'mutation($n:String!){ tesla { publishMap(name:$n){ name status } } }' "$(jq -n --arg n "$tesla_id" '{n:$n}')")
check_ok "tesla publish" "$resp"
assert_jq "tesla published" "$resp" '.data.tesla.publishMap.status=="PUBLISHED"'

resp=$(gql "$TOKEN_B" 'mutation($n:String!){ tesla { publishMap(name:$n){ name } } }' "$(jq -n --arg n "$tesla_id" '{n:$n}')")
check_code "tesla B publish" "$resp" FORBIDDEN

resp=$(gql "$TOKEN_B" 'query { tesla { maps { mapId status ownedByMe } } }')
check_ok "tesla B sees published" "$resp"
assert_jq "tesla B published visibility" "$resp" --arg id "$tesla_id" '.data.tesla.maps[] | select(.mapId==$id) | .status=="PUBLISHED" and .ownedByMe==false'

resp=$(gql "$TOKEN_A" 'mutation($n:String!){ tesla { createSession(mapID:$n){ id } } }' "$(jq -n --arg n "$tesla_id" '{n:$n}')")
check_ok "tesla createSession" "$resp"
TESLA_SESSION=$(printf '%s' "$resp" | jq -r '.data.tesla.createSession.id')
[ -n "$TESLA_SESSION" ] && [ "$TESLA_SESSION" != "null" ] || fail "tesla session id missing"
echo "smoke: tesla lifecycle ok (session $TESLA_SESSION)"

# ---- Mazes ----------------------------------------------------------------

MAZES_MAP=$(jq --arg id "$mazes_id" --arg name "Smoke Maze $suffix" '.id=$id | .name=$name' "$MAZES_FIXTURE")
mazes_vars=$(jq -n --argjson m "$MAZES_MAP" '{m:$m}')

resp=$(gql "" 'mutation($m:Mazes_MapInput!){ mazes { createMap(map:$m){ id status } } }' "$mazes_vars")
check_code "mazes guest create" "$resp" UNAUTHENTICATED

resp=$(gql "$TOKEN_A" 'mutation($m:Mazes_MapInput!){ mazes { createMap(map:$m){ id status ownedByMe } } }' "$mazes_vars")
check_ok "mazes createMap" "$resp"
CREATED_MAZES=1
assert_jq "mazes createMap draft" "$resp" '.data.mazes.createMap.status=="DRAFT" and .data.mazes.createMap.ownedByMe==true'

resp=$(gql "$TOKEN_A" 'query { mazes { maps { id status ownedByMe } } }')
check_ok "mazes A maps" "$resp"
assert_jq "mazes A sees own draft" "$resp" --arg id "$mazes_id" '.data.mazes.maps[] | select(.id==$id) | .status=="DRAFT" and .ownedByMe==true'

resp=$(gql "$TOKEN_B" 'query { mazes { maps { id status ownedByMe } } }')
check_ok "mazes B maps" "$resp"
assert_jq "mazes B cannot see A draft" "$resp" --arg id "$mazes_id" '[.data.mazes.maps[] | select(.id==$id)] | length==0'

resp=$(gql "$TOKEN_B" 'mutation($m:Mazes_MapInput!){ mazes { updateMap(map:$m){ id } } }' "$mazes_vars")
check_code_any "mazes B update" "$resp" "FORBIDDEN NOT_FOUND"
resp=$(gql "$TOKEN_B" 'mutation($id:ID!){ mazes { deleteMap(id:$id) } }' "$(jq -n --arg id "$mazes_id" '{id:$id}')")
check_code_any "mazes B delete" "$resp" "FORBIDDEN NOT_FOUND"

resp=$(gql "$TOKEN_A" 'mutation($id:ID!){ mazes { publishMap(id:$id){ id status } } }' "$(jq -n --arg id "$mazes_id" '{id:$id}')")
check_ok "mazes publish" "$resp"
assert_jq "mazes published" "$resp" '.data.mazes.publishMap.status=="PUBLISHED"'
resp=$(gql "$TOKEN_B" 'mutation($id:ID!){ mazes { publishMap(id:$id){ id } } }' "$(jq -n --arg id "$mazes_id" '{id:$id}')")
check_code "mazes B publish" "$resp" FORBIDDEN

resp=$(gql "$TOKEN_B" 'query { mazes { maps { id status ownedByMe } } }')
check_ok "mazes B sees published" "$resp"
assert_jq "mazes B published visibility" "$resp" --arg id "$mazes_id" '.data.mazes.maps[] | select(.id==$id) | .status=="PUBLISHED" and .ownedByMe==false'

resp=$(gql "$TOKEN_A" 'mutation($id:ID!){ mazes { createSession(mapID:$id){ session { id } ownerToken } } }' "$(jq -n --arg id "$mazes_id" '{id:$id}')")
check_ok "mazes createSession" "$resp"
MAZES_SESSION=$(printf '%s' "$resp" | jq -r '.data.mazes.createSession.session.id')
MAZES_OWNER=$(printf '%s' "$resp" | jq -r '.data.mazes.createSession.ownerToken')
[ -n "$MAZES_SESSION" ] && [ "$MAZES_SESSION" != "null" ] || fail "mazes session id missing"
echo "smoke: mazes lifecycle ok (session $MAZES_SESSION)"

# ---- Delivery -------------------------------------------------------------

CITY_CONFIG=$(cat "$DELIVERY_FIXTURE")
city_vars=$(jq -n --arg id "$city_id" --arg c "$CITY_CONFIG" '{id:$id,c:$c}')

resp=$(gql "" 'mutation($id:ID!,$c:String!){ deliveryCreateCity(id:$id,config:$c){ id status } }' "$city_vars")
check_code "delivery guest create" "$resp" UNAUTHENTICATED

resp=$(gql "$TOKEN_A" 'mutation($id:ID!,$c:String!){ deliveryCreateCity(id:$id,config:$c){ id name status ownedByMe } }' "$city_vars")
check_ok "delivery createCity" "$resp"
CREATED_CITY=1
assert_jq "delivery createCity draft" "$resp" '.data.deliveryCreateCity.status=="DRAFT" and .data.deliveryCreateCity.ownedByMe==true'

resp=$(gql "$TOKEN_A" 'query { delivery { cities { id name status ownedByMe } } }')
check_ok "delivery A cities" "$resp"
assert_jq "delivery A sees own draft" "$resp" --arg id "$city_id" '.data.delivery.cities[] | select(.id==$id) | .status=="DRAFT" and .ownedByMe==true'

resp=$(gql "$TOKEN_B" 'query { delivery { cities { id name status ownedByMe } } }')
check_ok "delivery B cities" "$resp"
assert_jq "delivery B cannot see A draft" "$resp" --arg id "$city_id" '[.data.delivery.cities[] | select(.id==$id)] | length==0'

resp=$(gql "$TOKEN_B" 'mutation($id:ID!,$c:String!){ deliveryUpdateCity(id:$id,config:$c){ id } }' "$city_vars")
check_code_any "delivery B update" "$resp" "FORBIDDEN NOT_FOUND"
resp=$(gql "$TOKEN_B" 'mutation($id:ID!){ deliveryDeleteCity(id:$id) }' "$(jq -n --arg id "$city_id" '{id:$id}')")
check_code_any "delivery B delete" "$resp" "FORBIDDEN NOT_FOUND"

resp=$(gql "$TOKEN_A" 'mutation($id:ID!){ deliveryPublishCity(id:$id){ id status } }' "$(jq -n --arg id "$city_id" '{id:$id}')")
check_ok "delivery publish" "$resp"
assert_jq "delivery published" "$resp" '.data.deliveryPublishCity.status=="PUBLISHED"'
resp=$(gql "$TOKEN_B" 'mutation($id:ID!){ deliveryPublishCity(id:$id){ id } }' "$(jq -n --arg id "$city_id" '{id:$id}')")
check_code "delivery B publish" "$resp" FORBIDDEN

resp=$(gql "$TOKEN_B" 'query { delivery { cities { id name status ownedByMe } } }')
check_ok "delivery B sees published" "$resp"
assert_jq "delivery B published visibility" "$resp" --arg id "$city_id" '.data.delivery.cities[] | select(.id==$id) | .status=="PUBLISHED" and .ownedByMe==false'

resp=$(gql "$TOKEN_A" 'query($id:ID!){ delivery { cityConfig(id:$id) } }' "$(jq -n --arg id "$city_id" '{id:$id}')")
check_ok "delivery cityConfig" "$resp"
assert_jq "delivery cityConfig JSON" "$resp" '(.data.delivery.cityConfig | fromjson | .name) == "Harbor"'

resp=$(gql "$TOKEN_A" 'mutation($id:ID!){ deliveryCreateSession(cityId:$id){ session { id } ownerToken } }' "$(jq -n --arg id "$city_id" '{id:$id}')")
check_ok "delivery createSession" "$resp"
CITY_SESSION=$(printf '%s' "$resp" | jq -r '.data.deliveryCreateSession.session.id')
CITY_OWNER=$(printf '%s' "$resp" | jq -r '.data.deliveryCreateSession.ownerToken')
[ -n "$CITY_SESSION" ] && [ "$CITY_SESSION" != "null" ] || fail "delivery session id missing"
echo "smoke: delivery lifecycle ok (session $CITY_SESSION)"

echo "smoke: all map/city lifecycle checks passed"
