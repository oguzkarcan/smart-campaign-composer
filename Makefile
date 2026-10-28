.PHONY: help up down logs test fmt vet tidy migrate seed

GO_IMAGE := golang:1.26-alpine

# Use a local Go toolchain if there is one, otherwise run Go in Docker so that
# only Docker is required. The container runs as the host user, so files it
# writes (go.mod, go.sum) are not root-owned, and it shares the host's caches.
HOST_GOMODCACHE := $(HOME)/go/pkg/mod
HOST_GOCACHE := $(HOME)/.cache/go-build
ifeq ($(shell command -v go 2>/dev/null),)
GO := mkdir -p $(HOST_GOMODCACHE) $(HOST_GOCACHE) && \
	docker run --rm --user $(shell id -u):$(shell id -g) \
	-e CGO_ENABLED=0 -e HOME=/tmp -e GOCACHE=/gocache -e GOMODCACHE=/gomodcache \
	-v $(CURDIR)/backend:/src -w /src \
	-v $(HOST_GOMODCACHE):/gomodcache -v $(HOST_GOCACHE):/gocache \
	$(GO_IMAGE) go
else
GO := cd backend && go
endif

help: ## List targets
	@grep -E '^[a-z]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-8s %s\n", $$1, $$2}'

up: ## Build and start all services
	docker compose up --build

down: ## Stop all services and remove volumes
	docker compose down -v

logs: ## Follow service logs
	docker compose logs -f

test: ## Run backend tests (mock LLM, no API key needed)
	$(GO) test ./...

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
