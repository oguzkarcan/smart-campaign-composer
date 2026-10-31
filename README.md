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
make test    # backend and frontend tests (mock LLM, no API key, no running
             # stack needed; Go and Node run in Docker if not installed)
make test-integration  # DB tests and the end-to-end delivery test against the
                       # seeded compose Postgres (needs `make up`; pauses the
                       # compose worker while it runs)
make vet     # go vet
make fmt     # go fmt
make up      # docker compose up --build
make down    # stop and remove volumes (fresh database next time)
make help    # list all targets
```

Frontend development with hot reload (needs Node; the dev server proxies
`/api` to the API on :8080, override with `API_URL`):

```sh
docker compose up -d api worker   # backend stack
cd frontend && npm install && npm run dev   # http://localhost:3000
```

### What the tests prove

- **Unit (`make test`):** segment DSL validation and SQL compilation
  (injection-like input is rejected or parameterized), consent, content limits
  on the worst case after expansion, the LLM repair loop, the HTTP API, the
  provider client's response classes, backoff and retry decisions, the
  worker's rate limiter, retries and graceful shutdown.
- **End to end (`make test-integration`):** the real worker, Postgres queue
  and HTTP client against the mock provider with its 100/s limit and injected
  429s, 5xx and lost responses, plus one recipient rejected with a 400. It
  checks that every message ends sent or failed, the duplicate-delivery
  counter stays 0, every provider call is recorded, a permanent error fails at
  once, retries stop at 5 attempts, `Retry-After` is waited out, and the
  provider's own rate limit is never hit. Concurrent approvals queue each
  recipient once.

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

- **frontend**: Vue 3 single-page app served by nginx, which also proxies
  `/api/*` to the API (same origin, no CORS; SSE unbuffered).
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

## API

All bodies are JSON. Errors are `{"error": "...", "problems": [{"path", "message"}]}`;
`problems` lists validation failures by path (`sms.body`, `conditions[0].field`).

| Method & path                          | What it does |
| -------------------------------------- | ------------ |
| `POST /campaigns`                      | `{"brief", "channels": ["sms", "email"]}` → creates the campaign and generates its segment. `201` even if generation fails: the campaign stays `draft` with `last_error`. |
| `GET /campaigns/{id}`                  | Campaign with segment rule, audience counts, content, `variable_lengths` and `limits`. |
| `POST /campaigns/{id}/segment`         | Regenerates the segment (discards content). |
| `POST /campaigns/{id}/content`         | Generates content, streamed as SSE: `delta` `{"attempt", "text"}`, then `done` (campaign) or `error`. A higher `attempt` means a repair round started. Errors before streaming starts are plain JSON. |
| `PUT /campaigns/{id}/content/{channel}`| `{"subject", "body"}` → marketer edit, validated like generated content (`422` with `problems`). |
| `POST /campaigns/{id}/approve`         | No body. Re-validates the content against today's audience (`422` if it no longer fits), locks it, queues one message per consented recipient and channel, status → `sending`. `409` if not `content_ready`, edited meanwhile, or nobody is reachable. |
| `GET /campaigns`                       | Campaigns newest first with message counts. `?limit=` (1–200, default 50), `?before=<id>` for the next page. |
| `GET /campaigns/{id}/progress`         | Delivery progress: message counts by status (`totals` and per channel), top failure reasons, `done`. |
| `GET /campaigns/{id}/events`           | SSE for the live dashboard: a `progress` event now and on every change; ends after one with `"done": true` (close the `EventSource` then). |
| `GET /health`                          | `200` when the database is reachable, `503` otherwise. |
| `GET /metrics`                         | Prometheus text format: messages by channel/status, campaigns by status, provider calls by outcome and code, provider and LLM latency, LLM generations and tokens. |

Status codes: `400` bad input, `404` unknown campaign, `409` not allowed in the
campaign's status (e.g. after approval), `422` invalid content or model output,
`502` the LLM could not be reached.

**Live length counter:** `variable_lengths` (e.g. `{"first_name": 14, "city": 8}`)
is what each variable counts as in the server's worst-case check, measured on
the campaign's reachable audience. A template's worst-case length is its length
with each `{{variable}}` replaced by that many characters; compare it with
`limits` (`{"sms.body": 160, "email.subject": 80, "email.body": 5000}`).

## Repository layout

```
backend/
  cmd/{api,worker,mockprovider,seed}   one binary each, one Docker image
  internal/
    config/        environment configuration
    store/         Postgres connection
    httpapi/       HTTP routes, SSE, error mapping
    seed/          deterministic fake users and events
    segment/       audience rule DSL: validator, SQL compiler, consent
    content/       message content: validation, worst-case length, rendering
    llm/           LLM interface, prompts, repair loop, Claude and mock clients
    campaign/      campaign flow: brief → segment → content → edits, Postgres store
    mockprovider/  fake SMS/email provider
  migrations/      SQL schema (golang-migrate)
frontend/          Vue 3 + Vite + TypeScript + Tailwind app
  src/api/         typed API client, SSE reader
  src/lib/         segment rule → readable text, formatting
  src/views/       pages (new campaign, campaign)
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
- HTTP API for the campaign flow with SSE content streaming; `LLM_MODE`
  selects the mock or Claude client.
- Approval: content re-validated and locked, messages enqueued idempotently in
  the same transaction (rule AND consent AND campaign channels).
- Provider client: renders at send time, `messages.id` as idempotency key,
  classifies responses (sent / retryable with `Retry-After` / permanent).
- Worker: claims with `FOR UPDATE SKIP LOCKED` and a 30 s lease, 100 msg/s
  per channel, up to 5 attempts with jittered exponential backoff and
  `Retry-After`, lease recovery, campaign completion, graceful shutdown.
- `/metrics` (Prometheus), campaign list, delivery progress and a live SSE
  progress stream for the dashboard.

- Frontend scaffold: Vite + Vue 3 + TypeScript + Tailwind, nginx image with
  the `/api` proxy, typed API client for every route including both SSE
  streams.

- Brief form and campaign page: the segment rule shown as readable
  conditions (raw JSON on demand) with audience counts per campaign channel,
  retry and regenerate.

Not done yet:
- Frontend screens: content editing and approval, live dashboard.
