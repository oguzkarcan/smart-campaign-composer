# Smart Campaign Composer (Insider One case)

A system that turns a marketer's natural-language campaign brief into a target
audience and SMS/email messages using Claude, presents them for human approval,
sends them through a queue to a mock provider, and shows delivery status live.

## Stack
- Backend: Go, Postgres (also used as the queue via FOR UPDATE SKIP LOCKED)
- Frontend: Vue 3 + Vite + TypeScript + Tailwind, SSE for streaming and live dashboard
- LLM: `LLM_MODE=mock|claude` (default mock), model from `CLAUDE_MODEL`
  (default `claude-sonnet-5-5`), key from `ANTHROPIC_API_KEY` in `.env`
- Setup: `docker compose up` (includes seed data and mock provider); tests: `make test`
  (uses the `golang` Docker image when Go is not installed locally)

## Layout
- `backend/cmd/{api,worker,mockprovider,seed}`: one binary each; one worker process
- `backend/internal/`: `config`, `store`, `httpapi`, `segment` (DSL, validator,
  SQL compiler), `llm` (interface, claude, mock), `content`, `campaign`, `delivery`
- `backend/migrations/`, `frontend/`, `ai-sessions/`

## Non-negotiable rules
- LLM output is never executed as SQL. Claude returns a JSON segment DSL built from
  whitelisted fields and operators; the backend validates it and compiles it to
  parameterized SQL.
- Channel consent is enforced in the backend, never delegated to the LLM.
- SMS <= 160 characters, email subject <= 80 characters; personalization variables
  like {{first_name}} are supported (validate worst-case length after expansion).
- Invalid LLM output is sent back to Claude with the validation errors (max 2
  repair attempts), then surfaced as a clear error. Every attempt is stored in
  `llm_generations`.
- Only message content is editable by the marketer; edited content is
  re-validated before approval. Allowed variables: `{{first_name}}`, `{{city}}`.
- Users only receive channels they consented to, from the campaign's `channels`.
- Max 100 messages/second per channel. Idempotency: UNIQUE(campaign_id, user_id, channel)
  plus `messages.id` sent to the provider as an idempotency key.
- Retries: max 5 attempts, exponential backoff with jitter (1 s base, 60 s cap),
  honor Retry-After on 429, no retry on permanent errors; stuck "processing"
  messages are recovered via a 30 s lease timeout.
- `/health` and `/metrics` (Prometheus text format) are required.
- Tests must run without a real API key, using a mock LLM and a mock provider.
- No real messages are ever sent; all users are fake seed data.

## Working rules
- Plan first, then code. Work in small steps and run tests after each one.
- Record important decisions and their rationale in DECISIONS.md as we go.
- Never commit secrets: use .env (gitignored) and provide .env.example.
- All code, comments, commit messages and docs are written in English.
- Unfinished scope goes in the README. Session exports go in `ai-sessions/`
  (scrub API keys and personal data first).
## Step-by-step plan (Day 2–5)
Progress is tracked here: tick a step (`[x]`) in the same commit that finishes it.
Each step ends with `make test` → commit → push. The next unticked step is the
one to work on after a `/clear`.

Day 1 (done): repo skeleton, compose, schema, seed, mock provider, `/health`,
segment DSL + validator + SQL compiler + consent.

### Day 2 – LLM layer
- [x] 1. `internal/content`: strict JSON parse of `{"sms": {"body"}, "email":
  {"subject", "body"}}` for the campaign's channels only; variable parsing
  (`{{first_name}}`, `{{city}}`, spacing canonicalized, unknown/malformed
  rejected); worst-case length after expansion from per-variable max lengths;
  path-based problems (all at once, repair-friendly); `Render` for the worker.
- [x] 2. `internal/llm`: `Client` interface (segment + streaming content), versioned
  prompts (DSL schema, catalog values, limits), deterministic mock client,
  generic generate → validate → repair loop (max 2 repairs) that records every
  attempt via a recorder (→ `llm_generations`). Tests: success, repair success,
  failure after 2 repairs, every attempt recorded.
- [x] 3. `internal/llm` Claude client: official Go SDK (`anthropic-sdk-go`),
  streaming for all calls, refusal fallback, usage and latency captured. Tests
  with `httptest` (no real key).
- [x] 4. `internal/campaign` + store: create campaign from brief → segment →
  audience count (`audience_ready`); per-variable max lengths over the consented
  audience; content generation → `campaign_contents` (`content_ready`);
  marketer edit re-validated (`human_edited`, `version`++); `llm_generations` writes.
- [x] 5. `httpapi` routes: `POST /campaigns`, `GET /campaigns/{id}`,
  `POST /campaigns/{id}/content` (SSE stream of tokens, then validated result or
  error), `PUT /campaigns/{id}/content/{channel}`; wire `LLM_MODE` in config/api.
  Handler tests with the mock LLM. Update README/DECISIONS.

### Day 3 – Delivery pipeline
- [x] 1. Approval: `POST /campaigns/{id}/approve` re-validates content, locks it,
  enqueues messages with `INSERT … SELECT` (rule AND consent AND channel in
  campaign channels) `ON CONFLICT DO NOTHING`; status `approved`→`sending`.
- [x] 2. `internal/delivery` provider client: render at send time, `messages.id`
  as idempotency key, classify responses (ok / retryable 5xx+timeout / 429 with
  Retry-After / permanent 4xx).
- [x] 3. Worker: claim with `FOR UPDATE SKIP LOCKED` + 30 s lease, per-channel
  token bucket 100/s, record `message_attempts`, backoff with jitter (1 s base,
  60 s cap, max 5 attempts), lease recovery loop, campaign `completed` detection,
  graceful shutdown.
- [x] 4. `/metrics` (Prometheus text: messages by status/channel, provider
  attempts by outcome, LLM generations) and campaign status endpoint +
  `GET /campaigns/{id}/events` SSE for the live dashboard; `GET /campaigns` list.
- [ ] 5. Tests: retry/backoff math, 429 Retry-After, permanent errors, idempotent
  enqueue, rate limiter; integration test end to end against the mock provider
  (duplicate-delivery counter stays 0). Update README/DECISIONS.

### Day 4 – Vue frontend
- [ ] 1. Scaffold Vite + Vue 3 + TS + Tailwind, Dockerfile, compose service on
  :3000, typed API client, dev proxy to the API.
- [ ] 2. Brief form → segment rule (readable) + audience counts per channel.
- [ ] 3. Content view: streamed generation over SSE, edit with inline
  validation (worst-case length counters), approve.
- [ ] 4. Live dashboard over SSE: per-channel status counts, progress, failures;
  campaign list.
- [ ] 5. Loading, error and empty states; polish.

### Day 5 – Hardening and delivery
- [ ] 1. Fill test gaps for the critical behaviours (DSL injection-like input,
  consent, limits, idempotency, retries, rate limiter); `make test` green.
- [ ] 2. README (setup, architecture, API, unfinished scope) and DECISIONS final pass.
- [ ] 3. Fresh-clone `docker compose up` check, demo run with mock and Claude.
- [ ] 4. Session exports into `ai-sessions/` (scrub keys and personal data).
- [ ] 5. Optional if time: GitHub Actions CI running `make test`.
