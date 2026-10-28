# Decisions

Short notes on important decisions and why they were made. Newest decisions are
appended at the bottom of each section.

## Stack

- **Go backend.** The worker, rate limiter, retries and SSE are all concurrency
  problems; goroutines and `golang.org/x/time/rate` fit them directly, and each
  service ships as a single binary.
- **Postgres is both the database and the queue** (`FOR UPDATE SKIP LOCKED`).
  Message status, idempotency and retries live in one transactional store. One
  less moving part than Redis/RabbitMQ, and throughput is far above the 100 msg/s
  per channel we need.
- **Vue 3 + Vite + TypeScript + Tailwind** for the frontend.
- **SSE** for content streaming and the live dashboard. Updates are one-way
  (server → browser), so WebSockets add nothing.
- **Go toolchain runs in Docker** (`make test` uses the `golang` image when Go is
  not installed locally), so reviewers only need Docker.

## LLM

- **Model `claude-sonnet-5-5`, configurable via `CLAUDE_MODEL`.** Segment and
  content generation are short, structured tasks; Sonnet gives good quality with
  lower latency and cost than Opus.
- **`LLM_MODE=mock|claude`, default `mock`.** `docker compose up` and `make test`
  work without an API key; the mock is deterministic.
- **The LLM never produces SQL.** Claude returns a JSON segment DSL built from
  whitelisted fields and operators. The backend validates it and compiles it to
  parameterized SQL.
- **Validate, then repair.** Invalid output (schema, unknown field/variable,
  length limit) is sent back to Claude with the validation errors, at most 2
  repair attempts; after that the user sees a clear error. Every attempt is
  stored in `llm_generations` (raw output, parsed output, errors) for auditing.
- **Streaming:** content tokens are streamed to the UI over SSE as they arrive;
  the full JSON is validated server-side only once complete, and the UI then
  replaces the streamed preview with the validated result. Simpler than parsing
  partial JSON in the browser.

## Audience and consent

- **Campaigns have a `channels` field** (sms, email, or both).
- **A user only receives a channel they consented to.** The consent filter is
  applied by the backend on every audience query and cannot be disabled by the
  LLM's output. A user who consented to both channels, on a campaign with both
  channels, receives both.

## Content

- **Only message content is editable** by the marketer before approval; the
  segment rule is not. Edited content goes through the same validation as
  generated content before it can be approved.
- **Allowed variables:** `{{first_name}}`, `{{city}}`. Unknown variables are a
  validation error (which triggers a repair attempt).
- **Length limits are checked on the worst case after expansion:** each variable
  is replaced by the longest value in the target audience (or its fallback text
  if that is longer). SMS ≤ 160 characters, email subject ≤ 80.
- **160 is enforced literally as characters.** Note: Turkish letters (ş, ğ, ı)
  force UCS-2 encoding, where a single real SMS segment is only 70 characters.
  We follow the case's 160-character rule and document this as a known gap.

## Delivery

- **One worker process** with goroutines. This makes an in-process token bucket
  (100 msg/s per channel) correct; scaling to several worker replicas would need
  a shared limiter (Postgres or Redis).
- **Idempotency:** `UNIQUE(campaign_id, user_id, channel)` prevents duplicate
  rows; `messages.id` is sent to the provider as an idempotency key, which
  covers "provider accepted it, but the response was lost".
- **Retry:** up to 5 attempts, exponential backoff with jitter (1 s base, 60 s
  cap). 429 honours `Retry-After`; 5xx and timeouts are retried; other 4xx are
  permanent and fail immediately. After the last attempt the message is
  `failed`.
- **Crash recovery:** a claimed message gets a 30 s lease (`lease_until`); if the
  worker dies, messages stuck in `processing` are reclaimed after the lease
  expires.
- **`message_attempts`** records every provider call (status code, error,
  latency) for metrics and debugging.

## Observability

- **`/metrics` in Prometheus text format**, `/health` checks the DB connection.

## Repository

- **Default branch `main`.**
