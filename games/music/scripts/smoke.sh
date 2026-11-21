#!/usr/bin/env bash
# End-to-end HTTP smoke for the Music Region game.
#
# Runs the real cmd/drbrain binary with every game enabled, then exercises the
# solve flow over POST /graphql only (never a per-game API route), the
# graphql-ws subscription, /healthz and /music/llms.txt, a restart, and a run
# with music disabled. Items d-h plus the payload and shutdown checks live
# here; items a-c are covered by Go tests
# (games/music/graph/shipped_flow_test.go).
#
# There is no browser in this environment, so the five visually dependent
# behaviours (listen, play selection, switch by click, flips by click, solved
# banner) are not verified here; they are covered by the unit/jsdom tests and
# this API-level smoke.
#
# Usage: games/music/scripts/smoke.sh
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
cd "$root"

tmp="$(mktemp -d "${TMPDIR:-/tmp}/drbrain-music-smoke.XXXXXX")"
port="$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')"
base="http://127.0.0.1:$port"
server_pid=""

log() { printf 'smoke: %s\n' "$*"; }
fail() { printf 'smoke: FAIL: %s\n' "$*" >&2; exit 1; }

stop_server() {
  [ -n "$server_pid" ] || return 0
  kill -TERM "$server_pid" 2>/dev/null || true
  wait "$server_pid" 2>/dev/null || true
  server_pid=""
}
cleanup() { stop_server; rm -rf "$tmp"; }
trap cleanup EXIT

export DRBRAIN_ADDR="127.0.0.1:$port"
export DRBRAIN_STATIC_DIR="$tmp/static"
export DRBRAIN_ACCOUNTS_DIR="$tmp/accounts"
export DRBRAIN_SHUTDOWN_TIMEOUT="5s"
export DRBRAIN_AUTH_PASSWORD="false"
export MAZES_MAPS_DIR="$root/games/mazes/maps"
export MAZES_SESSIONS_DIR="$tmp/mazes/sessions"
export MAZES_SOLUTIONS_DIR="$tmp/mazes/solutions"
export MOTOR_MAPS_DIR="$root/games/motor/maps"
export MOTOR_USER_MAPS_DIR="$tmp/motor/maps-user"
export MOTOR_SESSIONS_DIR="$tmp/motor/sessions"
export MOTOR_SOLUTIONS_DIR="$tmp/motor/solutions"
export DELIVERY_CITY="$root/games/delivery/maps/city.json"
export DELIVERY_SESSIONS_DIR="$tmp/delivery/sessions"
export TESLA_CONFIG_DIR="$root/games/tesla/maps"
export TESLA_CUSTOM_MAPS_DIR="$tmp/tesla/custom"
export TESLA_SESSIONS_DIR="$tmp/tesla/sessions"
export MUSIC_SCORES_DIR="$root/games/music/scores"
export MUSIC_SESSIONS_DIR="$tmp/music/sessions"
mkdir -p "$DRBRAIN_STATIC_DIR" "$DRBRAIN_ACCOUNTS_DIR"

start_server() {
  DRBRAIN_GAMES="$1" "$tmp/drbrain" serve >"$tmp/server.log" 2>&1 &
  server_pid=$!
  for _ in $(seq 1 150); do
    if curl -fsS "$base/healthz" >/dev/null 2>&1; then return 0; fi
    if ! kill -0 "$server_pid" 2>/dev/null; then
      cat "$tmp/server.log" >&2
      return 1
    fi
    sleep 0.1
  done
  cat "$tmp/server.log" >&2
  return 1
}

# gql QUERY [VARIABLES-JSON] prints the raw GraphQL response.
gql() {
  jq -nc --arg q "$1" --argjson v "${2:-null}" '{query:$q, variables:$v}' |
    curl -fsS -H 'Content-Type: application/json' --data-binary @- "$base/graphql"
}

# expect_ok QUERY [VARIABLES-JSON] prints the response, failing on GraphQL errors.
expect_ok() {
  local resp
  resp="$(gql "$@")"
  if ! jq -e '(.errors // []) | length == 0' <<<"$resp" >/dev/null; then
    printf '%s\n' "$resp" | jq . >&2
    fail "GraphQL operation returned errors"
  fi
  printf '%s' "$resp"
}

field() { jq -r "$1" <<<"$2"; }

measure_id() { # 1-based index -> mNNN
  printf 'm%03d' "$1"
}

# solve_session SESSION_ID submits the target arrangement (m001..mNNN, no
# flips; the shipped excerpts are in target order) and prints the response.
solve_session() {
  local sid="$1" n slots
  n="$(field '.data.music.session.slots | length' "$(expect_ok 'query($id: ID!) { music { session(id: $id) { slots { position } } } }' "$(jq -nc --arg id "$sid" '{id:$id}')")")"
  slots="$(jq -nc --argjson n "$n" '[range(0;$n) | {measureID: ("m" + (("00" + ((. + 1) | tostring))[-3:])), flipH: false, flipV: false}]')"
  expect_ok 'mutation($id: ID!, $slots: [Music_PlacementInput!]!) { music { setArrangement(sessionID: $id, slots: $slots) { solved seq wins percentPerWin lastChange { kind a b } } } }' \
    "$(jq -nc --arg id "$sid" --argjson slots "$slots" '{id:$id, slots:$slots}')"
}

create_session() { # EXCERPT DIFFICULTY SEED
  expect_ok 'mutation($id: ID!, $d: Music_Difficulty!, $seed: Int) { music { createSession(excerptID: $id, difficulty: $d, displayName: "Smoke", seed: $seed) { id difficulty solved seq wins percentPerWin slots { position measureID flipH flipV } } } }' \
    "$(jq -nc --arg id "$1" --arg d "$2" --argjson seed "$3" '{id:$id, d:$d, seed:$seed}')"
}

# ---- websocket subscription client (graphql-ws legacy protocol) ------------
# Written to $tmp so the script stays a single file; `go run` resolves the
# repo's module dependencies.
cat >"$tmp/ws_sub.go" <<'GO'
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/gorilla/websocket"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: ws_sub URL SESSION_ID")
		os.Exit(2)
	}
	conn, _, err := websocket.DefaultDialer.Dial(os.Args[1], nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "dial:", err)
		os.Exit(1)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(20 * time.Second))

	send := func(v any) error {
		raw, err := json.Marshal(v)
		if err != nil {
			return err
		}
		return conn.WriteMessage(websocket.TextMessage, raw)
	}
	read := func() (map[string]any, error) {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return nil, err
		}
		var msg map[string]any
		return msg, json.Unmarshal(raw, &msg)
	}

	if err := send(map[string]any{"type": "connection_init"}); err != nil {
		fmt.Fprintln(os.Stderr, "init:", err)
		os.Exit(1)
	}
	ack, err := read()
	if err != nil || ack["type"] != "connection_ack" {
		fmt.Fprintf(os.Stderr, "ack: %v %v\n", ack["type"], err)
		os.Exit(1)
	}
	payload := map[string]any{
		"query":     `subscription($id: ID!) { musicSessionUpdated(sessionID: $id) { session { id seq } lastChange { kind } } }`,
		"variables": map[string]any{"id": os.Args[2]},
	}
	if err := send(map[string]any{"id": "1", "type": "start", "payload": payload}); err != nil {
		fmt.Fprintln(os.Stderr, "start:", err)
		os.Exit(1)
	}

	for received := 0; received < 2; {
		msg, err := read()
		if err != nil {
			fmt.Fprintln(os.Stderr, "read:", err)
			os.Exit(1)
		}
		if msg["type"] != "data" {
			continue
		}
		data, _ := msg["payload"].(map[string]any)["data"].(map[string]any)
		update, _ := data["musicSessionUpdated"].(map[string]any)
		session, _ := update["session"].(map[string]any)
		seq, _ := session["seq"].(float64)
		kind := "-"
		if lc, ok := update["lastChange"].(map[string]any); ok && lc != nil {
			if k, ok := lc["kind"].(string); ok {
				kind = k
			}
		}
		fmt.Printf("%d %s\n", int(seq), kind)
		received++
	}
}
GO

# ---- build ----------------------------------------------------------------
go build -o "$tmp/drbrain" ./cmd/drbrain
log "built $tmp/drbrain; port $port"

# ---- main run: all five games ---------------------------------------------
start_server "mazes,motor,tesla,delivery,music"
log "server up (all games)"

excerpts="$(expect_ok '{ music { excerpts { id title measures { id len notes { at dur pitch } } } } }')"
count="$(field '.data.music.excerpts | length' "$excerpts")"
[ "$count" = "8" ] || fail "expected 8 excerpts, got $count"
excerpt_id="$(field '.data.music.excerpts[0].id' "$excerpts")"
maxbars="$(field '[.data.music.excerpts[] | .measures | length] | max' "$excerpts")"
[ "$maxbars" -ge 2 ] && [ "$maxbars" -le 6 ] || fail "longest piece has $maxbars bars, want 2..6"
log "excerpts: $count (longest $maxbars bars, solving $excerpt_id)"

# ---- difficulty model: whole piece, flips per difficulty -------------------
bars="$(field '.data.music.excerpts[0].measures | length' "$excerpts")"
for spec in NOVICE:5 EXPERT:10 GENIUS:15; do
  d="${spec%%:*}"; pct="${spec##*:}"
  dcreated="$(create_session "$excerpt_id" "$d" 5)"
  n="$(field '.data.music.createSession.slots | length' "$dcreated")"
  [ "$n" = "$bars" ] || fail "$d slots=$n, want the whole piece ($bars bars)"
  [ "$(field '.data.music.createSession.percentPerWin' "$dcreated")" = "$pct" ] || fail "$d percentPerWin != $pct"
  [ "$(field '.data.music.createSession.solved' "$dcreated")" = "false" ] || fail "$d session starts solved"
  h="$(field '[.data.music.createSession.slots[] | select(.flipH)] | length' "$dcreated")"
  v="$(field '[.data.music.createSession.slots[] | select(.flipV)] | length' "$dcreated")"
  case "$d" in
    NOVICE) { [ "$h" = 0 ] && [ "$v" = 0 ]; } || fail "novice carries flips h=$h v=$v" ;;
    EXPERT) { [ "$h" -ge 1 ] && [ "$v" = 0 ]; } || fail "expert flips h=$h v=$v (want h>=1, v=0)" ;;
    GENIUS) { [ "$h" -ge 1 ] && [ "$v" -ge 1 ]; } || fail "genius flips h=$h v=$v (want both)" ;;
  esac
  log "difficulty: $d uses all $n bars, flips h=$h v=$v, percentPerWin=$pct"
done

# ---- (c) initial solve: Expert, wins 1 ------------------------------------
created="$(create_session "$excerpt_id" "EXPERT" 11)"
sid="$(field '.data.music.createSession.id' "$created")"
[ "$(field '.data.music.createSession.solved' "$created")" = "false" ] || fail "new session already solved"
solved="$(solve_session "$sid")"
[ "$(field '.data.music.setArrangement.solved' "$solved")" = "true" ] || fail "setArrangement did not solve"
[ "$(field '.data.music.setArrangement.wins' "$solved")" = "1" ] || fail "wins after first solve != 1"
[ "$(field '.data.music.setArrangement.percentPerWin' "$solved")" = "10" ] || fail "expert percentPerWin != 10"
[ "$(field '.data.music.setArrangement.lastChange.kind' "$solved")" = "SOLVE" ] || fail "lastChange.kind != SOLVE"
log "solve: solved=true wins=1 percentPerWin=10 kind=SOLVE"

# ---- (d) a non-solving mutation keeps wins; reset + solve reaches 2 --------
mutated="$(expect_ok 'mutation($id: ID!) { music { transformMeasure(sessionID: $id, position: 0, flipH: true, flipV: false) { solved wins seq lastChange { kind } } } }' "$(jq -nc --arg id "$sid" '{id:$id}')")"
[ "$(field '.data.music.transformMeasure.solved' "$mutated")" = "false" ] || fail "flip left the puzzle solved"
[ "$(field '.data.music.transformMeasure.wins' "$mutated")" = "1" ] || fail "wins changed on a non-solving mutation"
reset="$(expect_ok 'mutation($id: ID!) { music { resetArrangement(sessionID: $id) { solved wins lastChange { kind } } } }' "$(jq -nc --arg id "$sid" '{id:$id}')")"
[ "$(field '.data.music.resetArrangement.solved' "$reset")" = "false" ] || fail "reset left the puzzle solved"
[ "$(field '.data.music.resetArrangement.lastChange.kind' "$reset")" = "RESET" ] || fail "reset kind != RESET"
resolved="$(solve_session "$sid")"
[ "$(field '.data.music.setArrangement.wins' "$resolved")" = "2" ] || fail "wins after second solve != 2"
log "wins: 1 after first solve, still 1 after a flip, 2 after reset+solve"

# ---- (3) Genius payload size and sidecar round-trip ------------------------
# Every difficulty uses the whole piece, so the longest shipped piece produces
# the largest payload.
largest="$(field '[.data.music.excerpts[] | {id: .id, n: (.measures | length)}] | max_by(.n) | .id' "$excerpts")"
genius="$(create_session "$largest" "GENIUS" 13)"
gid="$(field '.data.music.createSession.id' "$genius")"
slots="$(field '.data.music.createSession.slots | length' "$genius")"
payload="$MUSIC_SESSIONS_DIR/$gid.json"
meta="$MUSIC_SESSIONS_DIR/$gid.meta.json"
[ -f "$payload" ] || fail "missing payload $payload"
[ -f "$meta" ] || fail "missing meta sidecar $meta"
bytes="$(wc -c <"$payload" | tr -d ' ')"
[ "$slots" -le 6 ] || fail "genius session has $slots slots, over the 6-bar Music Region cap"
[ "$bytes" -lt 262144 ] || fail "genius payload $bytes bytes is not well under the 1 MiB body cap"
jq -e --arg id "$gid" '.id == $id' "$meta" >/dev/null || fail "meta sidecar does not describe $gid"
jq -e --arg id "$gid" '.id == $id and (.slots | length) == '"$slots" "$payload" >/dev/null || fail "payload does not round-trip $gid"
log "payload: genius session $gid on $largest = $bytes bytes (+ $(wc -c <"$meta" | tr -d ' ') byte meta), $slots slots"

# ---- (e) subscription delivers one update with the mutation's seq ----------
# Use a separate session so the solved witness for the restart check (sid,
# wins 2) is left untouched.
sub_created="$(create_session "$excerpt_id" "NOVICE" 17)"
sub_sid="$(field '.data.music.createSession.id' "$sub_created")"
ws_url="ws://127.0.0.1:$port/graphql"
go run "$tmp/ws_sub.go" "$ws_url" "$sub_sid" >"$tmp/ws.out" 2>"$tmp/ws.err" &
ws_pid=$!
for _ in $(seq 1 200); do
  [ -s "$tmp/ws.out" ] && break
  kill -0 "$ws_pid" 2>/dev/null || break
  sleep 0.05
done
[ -s "$tmp/ws.out" ] || { cat "$tmp/ws.err" >&2; fail "subscription delivered no initial snapshot"; }
subscribed="$(expect_ok 'mutation($id: ID!) { music { transformMeasure(sessionID: $id, position: 1, flipH: false, flipV: true) { seq lastChange { kind } } } }' "$(jq -nc --arg id "$sub_sid" '{id:$id}')")"
mut_seq="$(field '.data.music.transformMeasure.seq' "$subscribed")"
wait "$ws_pid" || { cat "$tmp/ws.err" >&2; fail "subscription client failed"; }
ws_seq="$(awk 'NR==2 {print $1}' "$tmp/ws.out")"
ws_kind="$(awk 'NR==2 {print $2}' "$tmp/ws.out")"
[ "$ws_seq" = "$mut_seq" ] || fail "subscription seq $ws_seq, mutation seq $mut_seq"
[ "$ws_kind" = "TRANSFORM" ] || fail "subscription kind $ws_kind, want TRANSFORM"
log "subscription: initial seq $(awk 'NR==1 {print $1}' "$tmp/ws.out"), update seq $ws_seq kind $ws_kind"

# ---- (4) churn then a measured, clean shutdown -----------------------------
churn=20
for _ in $(seq 1 "$churn"); do
  churn_created="$(create_session "$excerpt_id" "NOVICE" 0)"
  churn_id="$(field '.data.music.createSession.id' "$churn_created")"
  solve_session "$churn_id" >/dev/null
done
for _ in 1 2 3; do
  go run "$tmp/ws_sub.go" "$ws_url" "$sid" >"$tmp/ws-cancel.out" 2>/dev/null &
  cancel_pid=$!
  for _ in $(seq 1 200); do
    [ -s "$tmp/ws-cancel.out" ] && break
    kill -0 "$cancel_pid" 2>/dev/null || break
    sleep 0.05
  done
  kill -TERM "$cancel_pid" 2>/dev/null || true
  wait "$cancel_pid" 2>/dev/null || true
done
shutdown_start="$(python3 -c 'import time; print(time.time())')"
stop_server
shutdown_end="$(python3 -c 'import time; print(time.time())')"
shutdown_s="$(python3 -c "print(round($shutdown_end - $shutdown_start, 2))")"
python3 -c "raise SystemExit(0 if $shutdown_s < 5 else 1)" || fail "shutdown took ${shutdown_s}s (timeout 5s)"
log "churn: $churn create+solve, 3 cancelled subscriptions; shutdown in ${shutdown_s}s (< 5s)"

# ---- (f) restart: persisted session still solved with wins 2 --------------
start_server "mazes,motor,tesla,delivery,music"
log "server restarted on the same sessions dir"
reloaded="$(expect_ok 'query($id: ID!) { music { session(id: $id) { id solved wins } } }' "$(jq -nc --arg id "$sid" '{id:$id}')")"
[ "$(field '.data.music.session.solved' "$reloaded")" = "true" ] || fail "persisted session lost solved state"
[ "$(field '.data.music.session.wins' "$reloaded")" = "2" ] || fail "persisted session lost wins=2"
genius_reloaded="$(expect_ok 'query($id: ID!) { music { session(id: $id) { id slots { position } } } }' "$(jq -nc --arg id "$gid" '{id:$id}')")"
[ "$(field '.data.music.session.id' "$genius_reloaded")" = "$gid" ] || fail "genius session did not reload"
log "restart: session $sid still solved with wins=2; genius $gid reloaded"

# ---- (g) healthz and llms.txt ---------------------------------------------
health="$(curl -fsS "$base/healthz")"
[ "$(field '.games.music' "$health")" = "ok" ] || fail "healthz music != ok"
[ "$(field '.status' "$health")" = "ok" ] || fail "healthz status != ok"
llms_code="$(curl -s -o "$tmp/llms.txt" -w '%{http_code}' "$base/music/llms.txt")"
[ "$llms_code" = "200" ] || fail "/music/llms.txt status $llms_code"
grep -q "Music Region" "$tmp/llms.txt" || fail "/music/llms.txt does not name the game"
log "healthz: status=ok music=ok; /music/llms.txt: 200 ($(wc -c <"$tmp/llms.txt" | tr -d ' ') bytes)"
stop_server

# ---- (h) disabled selection: unified error + 404 under /music --------------
start_server "mazes,motor,tesla,delivery"
log "server up without music"
disabled="$(gql '{ music { excerpts { id } } }')"
message="$(jq -r '.errors[0].message // empty' <<<"$disabled")"
[ "$message" = "game is not enabled" ] || fail "disabled music message = ${message:-<missing>}"
code="$(curl -s -o /dev/null -w '%{http_code}' "$base/music")"
[ "$code" = "404" ] || fail "GET /music with music disabled = $code, want 404"
[ "$(field '.games.music // "absent"' "$(curl -fsS "$base/healthz")")" = "absent" ] || fail "disabled music appears in healthz"
log "disabled: query error \"game is not enabled\", GET /music 404, absent from healthz"
stop_server

log "SMOKE OK"
