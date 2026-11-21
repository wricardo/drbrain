# Local dev loop for the single drbrain binary.
#
#   just restart              # stop drbrain on :8080, regen, rebuild, restart
#   just port=8081 restart    # same on another port
#
# Artifacts and logs live in tmp/ (gitignored): tmp/drbrain, tmp/drbrain.pid,
# tmp/drbrain.log. The server is started detached with DRBRAIN_STATIC_DIR=web/build.
set shell := ["bash", "-euo", "pipefail", "-c"]

port := env("DRBRAIN_PORT", "8181")
addr := "127.0.0.1:" + port
bin := "tmp/drbrain"
pidfile := "tmp/drbrain.pid"
logfile := "tmp/drbrain.log"

default:
    @just --list

# Regenerate per-game SDL, merged schema, gqlgen executors and web GraphQL types
regen:
    go generate ./games/... ./platform/account ./graph

# Build the SvelteKit app into web/build
build-web:
    npm run build

# Build the drbrain binary into tmp/drbrain
build:
    go build -o {{bin}} ./cmd/drbrain

# Stop the drbrain server listening on the port (refuses to kill other programs)
stop:
    #!/usr/bin/env bash
    set -euo pipefail
    pidfile="{{pidfile}}"
    port="{{port}}"

    victims=""
    add_victim() {
        case " $victims " in
            *" $1 "*) ;;
            *) victims="$victims $1" ;;
        esac
    }

    if [[ -f "$pidfile" ]]; then
        p="$(cat "$pidfile")"
        if kill -0 "$p" 2>/dev/null; then
            add_victim "$p"
        fi
    fi

    while IFS= read -r p; do
        [[ -z "$p" ]] && continue
        cmd="$(ps -p "$p" -o command= 2>/dev/null || true)"
        if [[ "$cmd" == *drbrain* ]]; then
            add_victim "$p"
        else
            echo "port $port is held by non-drbrain process $p: $cmd" >&2
            echo "refusing to kill it; rerun with a free port: just port=<n> restart" >&2
            exit 1
        fi
    done < <(lsof -nP -tiTCP:"$port" -sTCP:LISTEN 2>/dev/null || true)

    rm -f "$pidfile"
    if [[ -z "${victims// /}" ]]; then
        echo "no drbrain server listening on :$port"
        exit 0
    fi

    echo "stopping drbrain pid(s):$victims"
    for p in $victims; do
        kill -TERM "$p" 2>/dev/null || true
    done
    for _ in $(seq 1 100); do
        alive=0
        for p in $victims; do
            if kill -0 "$p" 2>/dev/null; then alive=1; fi
        done
        [[ "$alive" -eq 0 ]] && break
        sleep 0.1
    done
    for p in $victims; do
        kill -KILL "$p" 2>/dev/null || true
    done
    echo "stopped"

# Start the built binary detached and wait until it answers
start:
    #!/usr/bin/env bash
    set -euo pipefail
    mkdir -p tmp
    rm -f "{{pidfile}}"

    export DRBRAIN_ADDR="{{addr}}"
    export DRBRAIN_STATIC_DIR="web/build"
    export DRBRAIN_DEBUG="${DRBRAIN_DEBUG:-false}"
    export DRBRAIN_ACCOUNTS_DIR="tmp/state/accounts"
    # Signs Bearer tokens. Dev-only value; production must set a real secret.
    export DRBRAIN_AUTH_JWT_SECRET="${DRBRAIN_AUTH_JWT_SECRET:-dev-only-insecure-jwt-secret-change-me}"
    # Built-in maps come from the repo; sessions/accounts stay under tmp/.
    export MAZES_MAPS_DIR="games/mazes/maps"
    export MAZES_SESSIONS_DIR="tmp/state/mazes/sessions"
    export MOTOR_MAPS_DIR="games/motor/maps"
    export MOTOR_USER_MAPS_DIR="tmp/state/motor/maps-user"
    export MOTOR_SESSIONS_DIR="tmp/state/motor/sessions"
    export MOTOR_ACCOUNTS_DIR="tmp/state/motor/accounts"
    export MUSIC_SCORES_DIR="games/music/scores"
    export MUSIC_CUSTOM_SCORES_DIR="tmp/state/music/scores-custom"
    export MUSIC_SESSIONS_DIR="tmp/state/music/sessions"
    export TESLA_CONFIG_DIR="games/tesla/maps"
    export TESLA_SESSIONS_DIR="tmp/state/tesla/sessions"
    export TESLA_CUSTOM_MAPS_DIR="tmp/state/tesla/maps-custom"
    export DELIVERY_CITY="games/delivery/maps/city.json"
    export DELIVERY_SESSIONS_DIR="tmp/state/delivery/sessions"
    mkdir -p tmp/state/{mazes,motor/{maps-user,sessions,accounts},music/{sessions,scores-custom},tesla/{sessions,maps-custom},delivery/sessions}

    nohup "{{bin}}" serve >"{{logfile}}" 2>&1 &
    pid=$!
    echo "$pid" > "{{pidfile}}"

    for _ in $(seq 1 150); do
        code="$(curl -s -o /dev/null -w '%{http_code}' "http://{{addr}}/" 2>/dev/null || true)"
        if [[ -n "$code" && "$code" != "000" ]]; then
            echo "drbrain listening on {{addr}} (pid $pid, log {{logfile}})"
            exit 0
        fi
        if ! kill -0 "$pid" 2>/dev/null; then
            echo "drbrain exited during startup:" >&2
            tail -n 50 "{{logfile}}" >&2 || true
            exit 1
        fi
        sleep 0.1
    done

    echo "drbrain did not answer on http://{{addr}}/ within 15s" >&2
    tail -n 50 "{{logfile}}" >&2 || true
    exit 1

# Stop, regenerate, rebuild web + binary, start
restart: stop regen build-web build start
