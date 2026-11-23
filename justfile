# Local dev loop for the single drbrain binary.
#
#   just db-up                # start PostgreSQL from docker-compose.yaml (:55432)
#   just restart              # stop drbrain on :8181, regen, rebuild, restart
#   just port=8081 restart    # same on another port
#   just verify               # gofmt check, vet, lint, tests, schema drift, web checks
#
# Artifacts and logs live in tmp/ (gitignored): tmp/drbrain, tmp/drbrain.pid,
# tmp/drbrain.log. The server is started detached with DRBRAIN_STATIC_DIR=web/build.
set shell := ["bash", "-euo", "pipefail", "-c"]

port := env("DRBRAIN_PORT", "8181")
addr := "127.0.0.1:" + port
bin := "tmp/drbrain"
pidfile := "tmp/drbrain.pid"
logfile := "tmp/drbrain.log"
database_url := env("DRBRAIN_DATABASE_URL", "postgres://drbrain:drbrain@127.0.0.1:55432/drbrain?sslmode=disable")

default:
    @just --list

# --- setup -----------------------------------------------------------------

# Install Go modules and the npm workspace (run once after clone)
setup:
    go mod download
    npm ci

# --- database --------------------------------------------------------------

# Start the dev PostgreSQL from docker-compose.yaml and wait until healthy
db-up:
    docker compose up -d --wait postgres

# Stop the dev PostgreSQL (data stays in the pgdata volume; add -v to erase it)
db-down:
    docker compose down

# Run all Go tests against the dev PostgreSQL
test-db: db-up
    TEST_DATABASE_URL="{{database_url}}" go test ./...

# Delete every row from the loopback PostgreSQL database (schema/migrations kept)
clear-local-test-data:
    #!/usr/bin/env bash
    set -euo pipefail

    url="{{database_url}}"
    # No explicit override? Match what the server uses: .env wins.
    if [[ -z "${DRBRAIN_DATABASE_URL:-}" && -f .env ]]; then
        set -a
        # shellcheck disable=SC1091
        . ./.env
        set +a
        url="${DRBRAIN_DATABASE_URL:-$url}"
    fi

    # Refuse anything that is not obviously a local database.
    case "$url" in
        *@127.0.0.1:*|*@localhost:*|*@\[::1\]:*) ;;
        *)
            echo "refusing: DRBRAIN_DATABASE_URL is not a loopback database" >&2
            echo "  url: $url" >&2
            exit 1
            ;;
    esac

    command -v psql >/dev/null || {
        echo "psql not found; install PostgreSQL client tools" >&2
        exit 1
    }

    # Keeps schema_migrations. CASCADE covers dependent tables/rows.
    psql "$url" -v ON_ERROR_STOP=1 -c '
        TRUNCATE TABLE
            delivery_reputation_entries,
            delivery_order_history,
            delivery_cash_entries,
            delivery_sessions,
            platform_sessions,
            platform_maps,
            sessions,
            maps,
            accounts
        RESTART IDENTITY CASCADE
    '
    echo "cleared local test data (schema and migrations kept)"

# --- code generation -------------------------------------------------------

# Regenerate per-game SDL, merged schema, gqlgen executors and web GraphQL types
regen:
    go generate ./games/... ./platform/account ./graph

# Fail when the split schema, merged schema or generated TypeScript is stale
schema-check:
    go run ./cmd/split-schema -check
    go run ./cmd/merge-schema -check
    go run ./cmd/gentypes -check

# --- builds ----------------------------------------------------------------

# Build the SvelteKit app into web/build
build-web:
    npm run build

# Build the drbrain binary into tmp/drbrain
build:
    go build -o {{bin}} ./cmd/drbrain

# Remove local build artifacts (logs, binary, coverage, web bundle)
clean:
    rm -rf tmp coverage.out coverage.html web/build

# --- quality ---------------------------------------------------------------

# gofmt the module
fmt:
    gofmt -w $(find . -name '*.go' -not -path './node_modules/*' -not -path './web/*' -not -path './tmp/*')

# Fail if any Go file needs gofmt
fmt-check:
    #!/usr/bin/env bash
    set -euo pipefail
    out="$(gofmt -l $(find . -name '*.go' -not -path './node_modules/*' -not -path './web/*' -not -path './tmp/*'))"
    if [[ -n "$out" ]]; then
        echo "gofmt needed:" >&2
        echo "$out" >&2
        exit 1
    fi

# go vet ./...
vet:
    go vet ./...

# golangci-lint run (install: go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest)
lint:
    @command -v golangci-lint >/dev/null || { echo "golangci-lint not installed" >&2; exit 1; }
    golangci-lint run

# Run all Go tests
test:
    go test ./...

# Run all Go tests with the race detector
test-race:
    go test -race ./...

# Write coverage.html from the Go tests
cover:
    go test ./... -coverprofile=coverage.out
    go tool cover -html=coverage.out -o coverage.html
    @echo "wrote coverage.html"

# Type check and unit test the web app
check-web:
    npm run check
    npm test

# Everything CI gates on: format, vet, lint, Go tests, schema drift, web checks
verify: fmt-check vet lint test schema-check check-web

# --- server ----------------------------------------------------------------

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

    # .env (gitignored) can override every default below. PostgreSQL is
    # required: accounts, sessions and custom maps/cities live there; only
    # builtin maps/cities, score libraries and the built web app are files.
    if [[ -f .env ]]; then
        set -a
        # shellcheck disable=SC1091
        . ./.env
        set +a
    fi

    export DRBRAIN_ADDR="${DRBRAIN_ADDR:-{{addr}}}"
    export DRBRAIN_STATIC_DIR="${DRBRAIN_STATIC_DIR:-web/build}"
    export DRBRAIN_DEBUG="${DRBRAIN_DEBUG:-false}"
    export DRBRAIN_DATABASE_URL="${DRBRAIN_DATABASE_URL:-{{database_url}}}"
    # Signs Bearer tokens. Dev-only value; production must set a real secret.
    export DRBRAIN_AUTH_JWT_SECRET="${DRBRAIN_AUTH_JWT_SECRET:-dev-only-insecure-jwt-secret-change-me}"
    # Builtin maps/cities and the score library stay files; everything else is
    # in PostgreSQL. No custom map/city/session directory is created here.
    export MAZES_MAPS_DIR="${MAZES_MAPS_DIR:-games/mazes/maps}"
    export MOTOR_MAPS_DIR="${MOTOR_MAPS_DIR:-games/motor/maps}"
    export MUSIC_SCORES_DIR="${MUSIC_SCORES_DIR:-games/music/scores}"
    export MUSIC_CUSTOM_SCORES_DIR="${MUSIC_CUSTOM_SCORES_DIR:-tmp/state/music/scores-custom}"
    export TESLA_CONFIG_DIR="${TESLA_CONFIG_DIR:-games/tesla/maps}"
    export DELIVERY_CITIES_DIR="${DELIVERY_CITIES_DIR:-games/delivery/maps}"
    export DELIVERY_CITY="${DELIVERY_CITY:-games/delivery/maps/city.json}"
    mkdir -p tmp/state/music/scores-custom

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

# --- observability ---------------------------------------------------------

# Start or update the Prometheus + Perses observability stack
obs-up:
    docker compose -f deploy/observability/compose.yaml up -d

# Render the Go dashboard definition into provisioning JSON
perses-build:
    #!/usr/bin/env bash
    set -euo pipefail
    cd deploy/observability/perses/dac
    tmp="$(mktemp)"
    trap 'rm -f "$tmp"' EXIT
    go run . --output json >"$tmp"
    # createdAt/updatedAt/version are API metadata, not dashboard code.
    jq 'del(.metadata.createdAt, .metadata.updatedAt, .metadata.version)' "$tmp" >"$tmp.clean"
    mv -f "$tmp.clean" ../provisioning/dashboard.json

# Apply Perses provisioning files via percli (auth off, loopback only)
perses-apply: perses-build
    docker compose -f deploy/observability/compose.yaml exec -e HOME=/tmp perses percli login http://localhost:8080
    docker compose -f deploy/observability/compose.yaml exec -e HOME=/tmp perses percli lint -d /etc/perses/provisioning --online
    docker compose -f deploy/observability/compose.yaml exec -e HOME=/tmp perses percli apply -d /etc/perses/provisioning

# --- compose ---------------------------------------------------------------

# Stop, regenerate, rebuild web + binary, start
restart: stop regen build-web build start

alias generate := regen
