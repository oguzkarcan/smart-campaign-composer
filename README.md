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
make test-integration  # also runs DB tests against the seeded compose Postgres
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
    segment/       audience rule DSL: validator, SQL compiler, consent
    content/       message content: validation, worst-case length, rendering
    llm/           LLM interface, prompts, repair loop, Claude and mock clients
    campaign/      campaign flow: brief → segment → content → edits, Postgres store
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
- Segment DSL with strict validation, SQL compiler and consent enforcement.
- Content validation: channel limits on the worst case after variable
  expansion, allowed variables only, rendering.
- LLM layer: versioned prompts, validate-and-repair loop (max 2 repairs) that
  records every attempt, streaming Claude client (official Go SDK) and a
  deterministic mock LLM.
- Campaign flow service: segment generation and audience counts, content
  generation measured on the reachable audience, re-validated marketer edits,
  every LLM attempt stored in `llm_generations`.

Not done yet:
- HTTP endpoints for the campaign flow, with SSE content streaming.
- Approval flow, worker sending, retries, `/metrics`.
- Vue frontend and live dashboard.
