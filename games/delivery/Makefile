# DrBrain Delivery — engine & tooling.
GO      ?= go
BINARY  ?= bin/drbrain

.PHONY: help
help: ## list targets
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## build the drbrain CLI
	$(GO) build -o $(BINARY) ./cmd/drbrain

.PHONY: run
run: ## print the city map
	$(GO) run ./cmd/drbrain map -stats

.PHONY: play
play: ## drive a car in the terminal
	$(GO) run ./cmd/drbrain play

.PHONY: serve
serve: ## run the HTTP API on :8080
	$(GO) run ./cmd/drbrain-server -addr localhost:8080

.PHONY: sim
sim: ## run the pacing harness (3000 ticks)
	$(GO) run ./cmd/drbrain sim -ticks 3000 -every 300

.PHONY: frontend
frontend: ## install frontend deps
	cd frontend && npm install

.PHONY: build-frontend
build-frontend: ## build the web UI into frontend/build
	cd frontend && npm install && npm run build

.PHONY: serve-ui
serve-ui: ## build the UI and serve API + UI on :8080
	cd frontend && npm run build
	cd $(CURDIR) && $(GO) run ./cmd/drbrain-server -addr localhost:8080

.PHONY: verify-ui
verify-ui: ## headless-Chrome smoke test of the UI (needs a built UI)
	$(GO) build -o $(BINARY)-server ./cmd/drbrain-server
	@# The server serves frontend/build. Rebuilding only the Go binary tests a
	@# stale bundle: a Svelte or api.ts change then passes the smoke test while
	@# being absent from what the browser actually loaded.
	cd frontend && npm run build
	@bash scripts/verify-ui.sh

.PHONY: check-frontend
check-frontend: types-check ## typecheck + build the frontend
	cd frontend && npm run check && npm run build

.PHONY: test
test: ## run all tests
	$(GO) test ./...

.PHONY: test-v
test-v: ## run tests verbosely
	$(GO) test ./... -v

.PHONY: cover
cover: ## write coverage.html
	$(GO) test ./... -coverprofile=coverage.out
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "wrote coverage.html"

.PHONY: fmt
fmt: ## gofmt the tree
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*')

.PHONY: vet
vet: ## go vet
	$(GO) vet ./...

.PHONY: generate
generate: ## run gqlgen (Go types from the schema)
	$(GO) run github.com/99designs/gqlgen generate

.PHONY: types
types: ## regenerate frontend/src/lib/api-types.ts from the GraphQL schema
	$(GO) run ./cmd/gentypes

.PHONY: types-check
types-check: ## fail if the generated types are stale
	$(GO) run ./cmd/gentypes -check

.PHONY: check
check: fmt vet generate types-check test ## fmt + vet + regenerate + typecheck

.PHONY: clean
clean: ## remove build artefacts
	rm -rf bin coverage.out coverage.html
