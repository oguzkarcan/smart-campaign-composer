.PHONY: help up down logs test fmt vet migrate seed

GO_IMAGE := golang:1.26-alpine

# Use a local Go toolchain if there is one, otherwise run Go in Docker so that
# only Docker is required.
ifeq ($(shell command -v go 2>/dev/null),)
GO := docker run --rm -e CGO_ENABLED=0 \
	-v $(CURDIR)/backend:/src -w /src \
	-v scc-gomod:/go/pkg/mod -v scc-gocache:/root/.cache/go-build \
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

migrate: ## Apply database migrations
	docker compose run --rm migrate

seed: ## Re-seed fake users and events
	docker compose run --rm seed
