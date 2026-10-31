.PHONY: help up down logs test test-integration fmt vet tidy migrate seed

GO_IMAGE := golang:1.26-alpine

# Use a local Go toolchain if there is one, otherwise run Go in Docker so that
# only Docker is required. The container runs as the host user, so files it
# writes (go.mod, go.sum) are not root-owned, and it shares the host's caches.
HOST_GOMODCACHE := $(HOME)/go/pkg/mod
HOST_GOCACHE := $(HOME)/.cache/go-build
# Integration tests reach Postgres from the compose network (Docker) or the
# published port (local Go).
ifeq ($(shell command -v go 2>/dev/null),)
GO = mkdir -p $(HOST_GOMODCACHE) $(HOST_GOCACHE) && \
	docker run --rm --user $(shell id -u):$(shell id -g) $(GO_DOCKER_FLAGS) \
	-e CGO_ENABLED=0 -e HOME=/tmp -e GOCACHE=/gocache -e GOMODCACHE=/gomodcache \
	-v $(CURDIR)/backend:/src -w /src \
	-v $(HOST_GOMODCACHE):/gomodcache -v $(HOST_GOCACHE):/gocache \
	$(GO_IMAGE) go
INTEGRATION_FLAGS := GO_DOCKER_FLAGS="--network scc_default \
	-e TEST_DATABASE_URL=postgres://scc:scc@postgres:5432/scc?sslmode=disable"
else
GO = cd backend && go
INTEGRATION_FLAGS := GO="cd backend && TEST_DATABASE_URL='postgres://scc:scc@localhost:$${POSTGRES_HOST_PORT:-5432}/scc?sslmode=disable' go"
endif

help: ## List targets
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-17s %s\n", $$1, $$2}'

up: ## Build and start all services
	docker compose up --build

down: ## Stop all services and remove volumes
	docker compose down -v

logs: ## Follow service logs
	docker compose logs -f

test: ## Run backend tests (mock LLM, no API key needed)
	$(GO) test ./...

# The compose worker is paused meanwhile, so it does not take the tests'
# queued messages; it is started again afterwards, even if the tests fail.
test-integration: ## Run DB integration tests against the seeded compose Postgres
	docker compose run --rm migrate
	docker compose run --rm seed
	docker compose stop worker
	$(MAKE) --no-print-directory _test-integration $(INTEGRATION_FLAGS); \
	status=$$?; docker compose start worker >/dev/null 2>&1; exit $$status

_test-integration:
	$(GO) test -tags integration -count=1 -p 1 ./... # packages share the database

fmt: ## Format Go code
	$(GO) fmt ./...

vet: ## Run go vet
	$(GO) vet ./...

tidy: ## Sync go.mod and go.sum with the code
	$(GO) mod tidy

migrate: ## Apply database migrations
	docker compose run --rm migrate

seed: ## Re-seed fake users and events
	docker compose run --rm seed
