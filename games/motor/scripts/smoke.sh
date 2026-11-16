#!/usr/bin/env bash
# End-to-end smoke test: starts the server, drives a session to WON over
# GraphQL, checks /llms.txt and password sign-in, then shuts down.
# Usage: scripts/smoke.sh [port]
set -euo pipefail

PORT="${1:-9191}"
URL="http://127.0.0.1:${PORT}"
cd "$(dirname "$0")/.."

SESSIONS="$(mktemp -d)"
go build -o "${SESSIONS}/drbrain-motor-programming" .
"${SESSIONS}/drbrain-motor-programming" -port "${PORT}" -host 127.0.0.1 -sessions-dir "${SESSIONS}/sessions" -user-maps-dir "${SESSIONS}/maps-user" -accounts-dir "${SESSIONS}/accounts" >"${SESSIONS}/server.log" 2>&1 &
PID=$!
trap 'kill "${PID}" 2>/dev/null || true; wait "${PID}" 2>/dev/null || true; rm -rf "${SESSIONS}" "${JAR:-/nonexistent}"' EXIT

for _ in $(seq 1 50); do
  curl -sf "${URL}/healthz" >/dev/null && break
  sleep 0.1
done

JAR="$(mktemp)"
gql() { # gql '<query>' — one browser: cookies persist between calls (-c/-b).
  curl -sf -c "${JAR}" -b "${JAR}" -H 'Content-Type: application/json' -d "$(jq -nc --arg q "$1" '{query:$q}')" "${URL}/graphql"
}
fail() { echo "FAIL: $*" >&2; cat "${SESSIONS}/server.log" >&2; exit 1; }

echo "maps:"; gql '{ maps { id } }' | tee /dev/stderr | jq -e '.data.maps | length > 0' >/dev/null || fail "no maps"
echo

CREATED=$(gql 'mutation { createSession(mapID:"straight_line") { id ownerToken } }') || fail "createSession"
SID=$(echo "${CREATED}" | jq -er '.data.createSession.id') || fail "createSession id"
TOKEN=$(echo "${CREATED}" | jq -er '.data.createSession.ownerToken') || fail "createSession ownerToken"
echo "session: ${SID}"

gql "mutation { setProgram(sessionID:\"${SID}\", program:{main:[MOVE_FORWARD,MOVE_FORWARD,MOVE_FORWARD,MOVE_FORWARD,MOVE_FORWARD,MOVE_FORWARD,MOVE_FORWARD,EMPTY], subs:[[EMPTY,EMPTY,EMPTY,EMPTY]]}, ownerToken:\"${TOKEN}\") { id } }" \
  | jq -e '.errors == null' >/dev/null || fail "setProgram"

gql "mutation { run(sessionID:\"${SID}\", speedMs:50, ownerToken:\"${TOKEN}\") { playing } }" | jq -e '.errors == null' >/dev/null || fail "run"
STATUS=""
for _ in $(seq 1 100); do
  STATUS=$(gql "{ session(id:\"${SID}\") { vm { status steps } } }" | jq -r '.data.session.vm.status')
  [ "${STATUS}" = "WON" ] && break
  sleep 0.1
done
[ "${STATUS}" = "WON" ] || fail "session did not reach WON (status=${STATUS})"
echo "session WON"

# Playing is creator-only: the token returned by createSession is the credential.
gql "mutation { setProgram(sessionID:\"${SID}\", program:{main:[EMPTY],subs:[[EMPTY,EMPTY,EMPTY,EMPTY]]}) { id } }" \
  | jq -e '.errors[0].extensions.code == "FORBIDDEN"' >/dev/null || fail "a token-less setProgram was allowed"
gql "mutation { run(sessionID:\"${SID}\", speedMs:50) { id } }" \
  | jq -e '.errors[0].extensions.code == "FORBIDDEN"' >/dev/null || fail "a token-less run was allowed"
gql "query { canPlaySession(id:\"${SID}\") }" | jq -e '.data.canPlaySession == false' >/dev/null || fail "canPlaySession without the token"
gql "query { canPlaySession(id:\"${SID}\", ownerToken:\"${TOKEN}\") }" | jq -e '.data.canPlaySession == true' >/dev/null || fail "canPlaySession with the token"
# Watching stays open to anyone.
gql "{ session(id:\"${SID}\") { id } }" | jq -e ".data.session.id == \"${SID}\"" >/dev/null || fail "spectating was blocked"
echo "creator-only play ok"

# The win is on the map's leaderboard with server-recorded slots (7 moves = 7 slots) and steps.
gql "{ leaderboard(mapId:\"straight_line\", sessionId:\"${SID}\") { total yours { rank slots steps } } }" \
  | jq -e '.data.leaderboard.total >= 1 and .data.leaderboard.yours.rank == 1 and .data.leaderboard.yours.slots == 7 and .data.leaderboard.yours.steps == 7' >/dev/null || fail "leaderboard entry"
echo "leaderboard ok"

curl -sf "${URL}/llms.txt" | grep -q "Dr Brain" || fail "/llms.txt"
echo "llms.txt ok"

# Sign-in is on by default: the password form is offered and its mutations work.
gql '{ authProviders { id } }' | jq -e '.data.authProviders | map(.id) | index("password")' >/dev/null || fail "password sign-in not offered"
gql 'mutation { registerWithPassword(handle: "smoke_player", password: "tangerine-ladder-42") { handle } }' \
  | jq -e '.data.registerWithPassword.handle == "smoke_player"' >/dev/null || fail "password register"
gql '{ me { handle hasPassword } }' | jq -e '.data.me.hasPassword == true' >/dev/null || fail "the register response did not sign the browser in"
# The cookie came back on the mutation: the same session keeps the player signed in.
gql 'mutation { logInWithPassword(handle: "smoke_player", password: "tangerine-ladder-42") { handle } }' \
  | jq -e '.data.logInWithPassword.handle == "smoke_player"' >/dev/null || fail "password login"
gql 'mutation { logout }' | jq -e '.data.logout == true' >/dev/null || fail "logout"
gql '{ me { handle } }' | jq -e '.data.me == null' >/dev/null || fail "still signed in after logout"
echo "password sign-in ok"

# Creating a map needs a signed-in player; a built-in map still needs the admin key to change.
gql 'mutation { createMap(map:{id:"smoke_map", name:"Smoke", layout:[">..*","....","...."]}) { id } }' \
  | jq -e '.errors[0].extensions.code == "UNAUTHENTICATED"' >/dev/null || fail "guest createMap was allowed"
gql 'mutation { logInWithPassword(handle: "smoke_player", password: "tangerine-ladder-42") { handle } }' \
  | jq -e '.data.logInWithPassword.handle == "smoke_player"' >/dev/null || fail "sign in to create a map"
gql 'mutation { createMap(map:{id:"smoke_map", name:"Smoke", layout:[">..*","....","...."]}) { id } }' \
  | jq -e '.data.createMap.id == "smoke_map"' >/dev/null || fail "createMap"
gql 'mutation { updateMap(map:{id:"straight_line", name:"hijack", layout:[">..*","....","...."]}) { id } }' \
  | jq -e '.errors[0].extensions.code == "FORBIDDEN"' >/dev/null || fail "built-in map write was not gated"
echo "map creation ok"
echo "SMOKE OK"
