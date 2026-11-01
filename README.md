# Sirius Studio · Smart Campaign Composer
### Autonomous Multi-Channel Campaign & Delivery Engine for Insider One

[![CI](https://github.com/oguzkarcan/smart-campaign-composer/actions/workflows/ci.yml/badge.svg)](https://github.com/oguzkarcan/smart-campaign-composer/actions/workflows/ci.yml)

A marketer writes a campaign brief in plain language. Claude turns it into a target audience and per-language SMS/email copy, the marketer reviews and approves them in a zero-scroll studio, and a rate-limited Postgres queue delivers the messages to a chaos mock provider while a live dashboard streams progress.

> **Insider One · AI Developer Case Study.** All 5,000 recipients are deterministic synthetic users and delivery goes to a local mock provider.

| Light · EN (SMS Studio) | Dark · TR (Email Studio) |
| --- | --- |
| ![Message Studio in light mode: SMS editor and approval bar beside the phone preview](docs/images/studio-light.png) | ![Message Studio in dark mode: email editor and approval bar beside the inbox preview](docs/images/studio-dark.png) |

## Quick start

Requirements: Docker with Compose (Go and Node run in Docker for tests when not installed locally).

```sh
cp .env.example .env   # optional; defaults run in deterministic mock mode
docker compose up
```

Open **http://localhost:3000**. The header switches interface language (`TR | EN`) and theme (`Light | Dark`, defaulting to dark on first visit).

| Service       | URL                           | Notes |
| ------------- | ----------------------------- | ----- |
| Frontend      | http://localhost:3000         | Vue 3 + Tailwind v4 studio (`nginx` reverse proxy) |
| API & Health  | http://localhost:8080/health  | Reports DB status + active `llm_provider` / `llm_model` |
| Metrics       | http://localhost:8080/metrics | Prometheus text format |
| Mock provider | http://localhost:9090/stats   | 100/s limit, `Idempotency-Key` replay, chaos injection |
| Postgres      | `localhost:5432` (`scc`/`scc`)| Auto-migrated & seeded with 5,000 users + 90d events |

To use live Claude instead of the default mock LLM, set these in `.env` and restart `api`:

```sh
LLM_MODE=claude
ANTHROPIC_API_KEY=sk-ant-...        # gitignored
CLAUDE_MODEL=claude-sonnet-5-5      # default
```

## Product walkthrough

1. **New campaign (`/new`):** Write a natural-language brief (or pick a one-click sample) and select channels (`SMS`, `Email`, or both).
2. **Step 1 — Audience:** The brief compiles into a validated JSON segment DSL rendered as readable conditions (`TR`/`EN`), with reachable counts per channel and language plus a sortable 10-per-page **Matching people** table (`COLLATE "tr-TR-x-icu"`).
3. **Step 2 — Message Studio (zero-scroll):** Generates `tr` and `en` copy over SSE, streaming field-by-field live into the editor and device mockup (iPhone SMS bubble / Email inbox card). Pick any matched person to preview with their real name, city, and consent state. Inline counters enforce worst-case character limits (`{{first_name}}`, `{{city}}`).
4. **Approve or Schedule:** Send immediately or schedule up to 30 days ahead (`scheduled` campaigns show a live countdown with **Send now** and **Cancel schedule & edit** actions).
5. **Step 3 — Live Delivery:** Streams real-time delivery progress, throughput (`msg/s`), retries, and top failure reasons over SSE until `completed`.

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

**Campaign lifecycle:** `draft` → `audience_ready` → `content_ready` → (`scheduled` →) `sending` → `completed` / `failed`.

| Core guarantee | How it is enforced |
| -------------- | ------------------ |
| **LLM never writes SQL** | Model returns a strict JSON segment DSL (whitelisted fields, operators, catalog values); `internal/segment` validates and compiles it to parameterized SQL (`$1, $2…`). |
| **Validate & repair loop** | `llm.Generate` feeds validation errors back to the model (up to 2 repairs) and logs every attempt, token count, and latency in `llm_generations`. |
| **Consent outside the rule** | Recipient queries wrap the compiled rule in `WHERE (<rule>) AND <channel_opt_in>`, so an `OR` condition can never bypass channel consent. |
| **Worst-case length limits** | SMS ≤ 160 and Email subject ≤ 80 chars are checked against the longest real `first_name` and `city` in each language's reachable audience, backed by DB `CHECK` constraints and worker send-time checks. |
| **100 msg/s per channel** | Independent per-channel token buckets (`rate.Limiter`, 100/s, burst 10, 32 concurrent senders) in the worker; verified against the provider's own 100/s limiter. |
| **Zero duplicate deliveries** | `UNIQUE(campaign_id, user_id, channel)` on `messages` + `Idempotency-Key: <message_id>` replayed by the provider on lost responses + 30s worker lease recovery (`FOR UPDATE SKIP LOCKED`). |
| **Retry classification** | Up to 5 attempts with equal-jitter exponential backoff (1s base, 60s cap) + `Retry-After` on `429`/`503`; permanent `4xx` errors fail immediately. |

See **[DECISIONS.md](DECISIONS.md)** for the full architectural rationale and trade-off log.

## API overview

| Method & path | Description |
| ------------- | ----------- |
| `POST /campaigns` | `{"brief", "channels"}` → creates campaign and generates its segment (`201`). |
| `GET /campaigns` | Cursor-paginated list (`?limit=50&before=<id>`) with message status counts. |
| `GET /campaigns/{id}` | Full campaign with rule, per-language audience counts, content, and `variable_lengths`. |
| `POST /campaigns/{id}/segment` | Regenerates segment rule and audience snapshot (resets content). |
| `POST /campaigns/{id}/content` | Streams per-language content generation (`delta`, `done`, `error`) over SSE. |
| `PUT /campaigns/{id}/content/{lang}/{ch}` | Validates and saves marketer edits for one language and channel (`422` with `problems`). |
| `GET /campaigns/{id}/recipients` | Paginated (`?limit=10&offset=`), Turkish-ICU sorted (`?sort=first_name\|city\|language&order=asc\|desc`) matching users with consent flags. |
| `POST /campaigns/{id}/approve` | Optional `{"send_at": "<RFC3339>"}`. Re-validates, locks content, queues messages in SQL, transitions to `sending` or `scheduled`. |
| `DELETE /campaigns/{id}/schedule` | Cancels a `scheduled` campaign back to `content_ready` and removes queued messages in one locked transaction (`409` if already sending). |
| `POST /campaigns/{id}/send-now` | Immediately transitions a `scheduled` campaign to `sending` and makes its queued messages due now. |
| `GET /campaigns/{id}/progress` & `/events` | Snapshot JSON and live SSE stream of delivery progress and top failure reasons. |
| `GET /health` & `GET /metrics` | DB health + active LLM metadata (`200`/`503`), and Prometheus metrics exposition. |

```sh
# End-to-end CLI smoke run (mock LLM):
id=$(curl -s localhost:8080/campaigns -d '{"brief":"Users in Ankara who viewed a product this week","channels":["email"]}' | jq .id)
curl -sN -X POST localhost:8080/campaigns/$id/content
curl -s -X POST localhost:8080/campaigns/$id/approve
curl -sN localhost:8080/campaigns/$id/events
```

## Testing & commands

```sh
make test              # Go unit tests + vue-tsc + 57 Vitest frontend tests (no API key or running stack needed)
make test-integration  # Postgres integration suite + end-to-end worker/provider chaos delivery test
make vet               # go vet ./...
make up / make down    # Start stack / stop and wipe volumes for a fresh seed
```

- **Unit suite (`make test`):** SQL injection & consent-bypass rejection, worst-case Unicode length math, repair loop & fake Claude SSE server (streaming, refusal, truncation, 529 retries), incremental JSON stream extractor, retry/backoff math, and rate-limiter window bounds.
- **Integration & E2E (`make test-integration`):** Compiled DSL vs hand-written SQL on the 5,000-user seed, concurrent approval races, lease expiry recovery, scheduled send lifecycle, and a full chaos delivery run proving `duplicate_deliveries == 0` and `rate_limited == 0`.

## Repository layout

```
backend/
  cmd/{api,worker,mockprovider,seed}   4 binaries built into 1 Docker image
  internal/{segment,content,llm,campaign,delivery,metrics,httpapi,seed,mockprovider}
  migrations/                          000001..000005 SQL migrations (golang-migrate)
frontend/
  src/{api,lib,components,views}       Vue 3 + TypeScript + Tailwind v4 studio
ai-sessions/                           01..08 scrubbed Claude Code session transcripts
DECISIONS.md                           Detailed architecture & trade-off log
```

## Known limitations & production roadmap

- **Consent at claim time:** Consent is checked when queueing at approval; re-checking `users.<ch>_opt_in` in the worker's `Claim` query would cover opt-outs during a scheduled wait.
- **Distributed rate limiting:** The token bucket is per-worker process (`Claim` itself is multi-worker safe via `SKIP LOCKED`); multiple worker replicas would share a Redis/Postgres bucket.
- **SMS encoding:** Enforces the case's 160-character limit; production Turkish SMS (`ş, ğ, ı, İ`) would use a GSM-7 / UCS-2 (70-char) segment calculator.
- **A/B testing design:** Add `variant` (`A`/`B`) to `campaign_contents` and `messages`, assign recipients deterministically at approval via `hash(campaign_id, user_id) mod 100` so retries stay consistent and users get the same arm across channels, and attribute post-`sent_at` `purchase` events per variant.
