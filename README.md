# Smart Campaign Composer

A marketer writes a campaign brief in plain language. Claude turns it into a
target audience and SMS/email messages, the marketer reviews and approves them,
and the messages are sent through a queue to a mock provider while a dashboard
shows delivery status live.

> Insider One · AI Developer Case Study. No real messages are ever sent: all
> recipients are generated fake users and delivery goes to a local mock provider.

## Quick start

Requirements: Docker with Compose. Nothing else (Go runs in Docker for tests).

```sh
cp .env.example .env   # optional; everything has mock-mode defaults
docker compose up
```

| Service        | URL                          |
| -------------- | ---------------------------- |
| Frontend       | http://localhost:3000        |
| API            | http://localhost:8080/health |
| Mock provider  | http://localhost:9090/stats  |
| Postgres       | localhost:5432 (`scc`/`scc`) |

By default the app uses a deterministic **mock LLM**, so no API key is needed.
To use Claude, set `LLM_MODE=claude` and `ANTHROPIC_API_KEY` in `.env`.

## Commands

```sh
make test    # backend tests (mock LLM, no API key, no running stack needed)
make vet     # go vet
make fmt     # go fmt
make up      # docker compose up --build
make down    # stop and remove volumes (fresh database next time)
make help    # list all targets
```

## Architecture

```
            ┌──────────┐  SSE / REST  ┌───────┐   Claude API (or mock)
 browser ──▶│ frontend │─────────────▶│  api  │──────────────▶ LLM
            └──────────┘              └───┬───┘
                                          │ campaigns, messages (queue)
                                      ┌───▼──────┐
                                      │ Postgres │
                                      └───▲──────┘
                                          │ FOR UPDATE SKIP LOCKED
                                      ┌───┴────┐  100 msg/s per channel  ┌──────────────┐
                                      │ worker │────────────────────────▶│ mockprovider │
                                      └────────┘  retry + idempotency key└──────────────┘
```

- **api**: campaign endpoints, LLM calls, SSE streams, `/health`, `/metrics`.
- **worker**: claims due messages from Postgres, rate-limits per channel,
  retries with backoff, recovers expired leases.
- **mockprovider**: imitates an SMS/email provider with a real rate limit and
  configurable 429s, 5xx, lost responses and latency.
- **seed / migrate**: one-shot jobs run by compose before the services start.

Key rules: Claude's output is a JSON segment DSL that the backend validates and
compiles to parameterized SQL (never executed as SQL directly); channel consent
is enforced in the backend; channel length limits are checked on the worst case
after variable expansion. See [DECISIONS.md](DECISIONS.md) for the reasoning
behind every major choice.

## Repository layout

```
backend/
  cmd/{api,worker,mockprovider,seed}   one binary each, one Docker image
  internal/
    config/        environment configuration
    store/         Postgres connection
    httpapi/       HTTP routes
    seed/          deterministic fake users and events
    mockprovider/  fake SMS/email provider
  migrations/      SQL schema (golang-migrate)
frontend/          Vue 3 app (placeholder for now)
ai-sessions/       Claude session exports
```

## Status

Done:
- One-command setup: Postgres, migrations, seed, api, worker, mock provider.
- Schema with database-level backstops for the core rules.
- Deterministic seed: 5,000 users, consent mix, 90 days of events.
- Mock provider with rate limiting, failure injection and idempotency.
- API `/health` with a database check.

Not done yet:
- Segment DSL, validator and SQL compiler.
- LLM layer (Claude and mock clients, validation and repair).
- Content generation with streaming, limits and personalization.
- Approval flow, worker sending, retries, `/metrics`.
- Vue frontend and live dashboard.
