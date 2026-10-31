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

- **One small `Client` interface:** `Complete(request, onDelta)` with a system
  prompt and a list of messages. Segment and content generation are just
  different prompts, and a repair is the same call with the bad answer and the
  errors appended, so one method covers everything. Requests also carry the
  structured input they were built from; real clients ignore it, the mock uses
  it to answer deterministically.
- **The repair loop is generic** (`llm.Generate[T]` with a parse function), so
  segment rules and content share one implementation and one set of tests.
  Only validation errors are repaired; API/transport errors are recorded and
  returned immediately (retrying them is the client's job, not a repair).
  Failing to record an attempt fails the generation: the audit trail is a
  requirement. Attempts are recorded even if the request was cancelled
  meanwhile, so the trail matches what was actually spent.
- **Streamed text carries the attempt number,** so the UI can reset its preview
  when a repair round starts instead of showing two answers glued together.
- **Prompts are versioned** (`segment-v1`, `content-v1`); the version is stored
  with every attempt so quality can be compared across prompt changes.
- **The brief is treated as data.** It is wrapped in `<brief>` tags and the
  system prompt says to ignore instructions inside it. This only lowers the
  odds of prompt injection; the real guarantee is that every answer is
  validated (whitelisted DSL, allowed variables, limits) and never executed.
- **The prompt states the limits with the audience's real numbers** (e.g.
  `{{first_name}}` counts as 15 characters), so Claude can write to the worst
  case instead of learning it through a repair round.
- **Prose and code fences around the JSON are trimmed** before parsing, a cheap
  fix for the most common harmless formatting slip; anything else goes through
  validation and repair.
- **The mock is a keyword reader, not random.** It finds catalog cities, cart,
  purchase (and its negation), visits, "N days" and an explicitly named
  language ("English", "Türkçe"; the language the brief is written in is not a
  filter) in Turkish or English briefs. Content uses fixed templates short
  enough to pass even with unmeasured variable lengths, and carries a discount
  from the brief (`%30`, `30%`, `yüzde 30`) in the language's own style
  ("%30 indirim", "30% off"). Like the real prompt, it uses only facts from the
  brief: no discount is mentioned unless the brief has one. The case's example brief gives the same rule as Claude would. A
  separate `Scripted` client drives repair and failure paths in tests.

- **Official Anthropic Go SDK** for the Claude client, not hand-written HTTP:
  typed requests and stream events, and its built-in retries (429, 529
  overload, 5xx, connection errors, with backoff honouring `retry-after`).
  API-level retries therefore live in the client, separate from repair rounds.
- **Always streaming.** Segment and content calls both stream: text deltas go
  to the UI, and the accumulated message gives usage and the stop reason.
- **Adaptive thinking at `low` effort, no sampling parameters.** These are
  short structured tasks; Sonnet 5.5 cannot disable thinking, and low effort
  keeps latency and cost down. Raising it is a one-line change if quality needs it.
- **Refusals:** server-side refusal fallback (`fallbacks: "default"`, beta
  `server-side-fallback-2026-07-01`) re-serves a declined request on a fallback
  model in the same call, and the model that actually answered is stored with
  the attempt. A refusal that still comes back is a clear error, not a repair.
- **A `max_tokens` cut-off is an error, not a repair:** the truncated answer is
  stored, but asking again with the same limit would most likely be cut off again.
- **JSON by prompt + our validator, not API structured outputs.** The real
  rules (catalog values, worst-case lengths after expansion) cannot be expressed
  in a JSON schema, so our validator is the source of truth either way.
  Structured outputs could remove pure syntax repairs; noted as a possible
  improvement.
- **No prompt caching yet:** the system prompts are below the minimum
  cacheable size, so a cache breakpoint would do nothing.

## Segment DSL

- **Small, explicit grammar.** A rule is an `all`/`any` group of conditions;
  each condition is a field comparison (`city`, `language` with `eq`, `neq`,
  `in`, `not_in`), an event condition (`page_view`, `cart_add`, `purchase` with
  `within_days`, `exists`, optional `min_count`) or a nested group. Limits: depth
  3, 20 conditions, 50 values, 365 days. A small grammar is easier for Claude to
  get right and easier to validate completely.
- **Strict decoding.** Unknown keys anywhere are rejected (an extra `"sql"` key
  is an error, not ignored), as is trailing data, mixing condition kinds, or
  keys that don't belong to the condition's kind.
- **Values are checked against the database.** The validator loads the cities
  and languages that actually exist. Spelling is canonicalized ignoring case and
  Turkish diacritics (`Istanbul`, `ISTANBUL` → `İstanbul`); an unknown value is
  an error listing the allowed values, which gives the repair prompt something
  concrete to fix. A brief for a city with no users fails clearly instead of
  silently matching nobody.
- **All problems are reported at once, with paths** like
  `conditions[1].within_days`, so one repair round can fix everything.
- **Defense in depth in the compiler.** It does not trust validation: column
  names come only from a whitelist map, unknown names are an error, and every
  value (including the `within_days` cutoff time) is a positional argument.
- **Time is a parameter, not `now()`.** The cutoff is computed in Go from a
  passed-in time, so compiled SQL is deterministic in tests.
- **Consent is outside the rule.** Recipient queries are
  `WHERE (<rule>) AND <consent column>`, so an `any` (OR) rule cannot bypass
  consent. The consent column comes from a channel → column map.
- **Integration tests compare against hand-written SQL.** `make
  test-integration` runs compiled rules on the seeded database and checks the
  counts against an independent reference query. `make test` stays free of
  external dependencies.

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
  is replaced by the longest value in the target audience. SMS ≤ 160 characters,
  email subject ≤ 80, email body ≤ 5,000. `first_name` and `city` are `NOT NULL`,
  so there is no fallback text. A variable whose maximum was not measured (or is
  0, e.g. an empty audience) counts as 40 characters, so a missing measurement
  can only make validation stricter. A variable never counts as shorter than its
  placeholder (`{{first_name}}` is 14 characters): the database `CHECK` limits the
  stored raw template, so with short names the expanded message could fit while
  the insert fails. Email subjects reject both `\r` and `\n`.
- **Variables are canonicalized, stray braces are rejected.** `{{ first_name }}`
  is rewritten to `{{first_name}}`; `{first_name}`, unbalanced or stray braces and
  unknown names like `{{.Phone}}` are errors. Braces outside a variable would
  reach recipients verbatim, and a template engine is never used for rendering
  (plain substitution of the two allowed names), so content cannot run code.
- **Content JSON is decoded strictly, like the segment rule.** Unknown keys, a
  subject on SMS, a missing campaign channel or an extra one are all errors, and
  every problem is reported with a path (`sms.body`) and, for length errors, how
  many characters to cut and how each variable was counted, so one repair round
  has what it needs.
- **160 is enforced literally as characters.** Note: Turkish letters (ş, ğ, ı)
  force UCS-2 encoding, where a single real SMS segment is only 70 characters.
  We follow the case's 160-character rule and document this as a known gap.

## Campaign flow

- **Statuses:** `draft` → `audience_ready` (segment generated and counted) →
  `content_ready` → `approved` → `sending` → `completed`/`failed`. A failed
  generation keeps the status and stores `last_error`, so the marketer can retry;
  a later success clears it.
- **Channels are chosen by the marketer, never by the LLM,** and are validated
  and stored in a canonical order when the campaign is created.
- **A new segment discards the content.** Content is written and length-checked
  for a specific audience; after the audience changes it must be regenerated.
- **Content needs a reachable audience.** If nobody who matches consented to the
  campaign's channels, content generation is refused with an explanation
  instead of writing messages nobody will receive.
- **Variable lengths are measured on the reachable audience:** users the rule
  matches who consented to at least one campaign channel. Measuring over both
  channels at once is slightly conservative for a single channel, and keeps one
  set of numbers per campaign. Edits are re-measured against the audience as it
  is at edit time; approval will measure again.
- **Audience counts are a snapshot** (`audience_count`, `audience_sms`,
  `audience_email`) taken when the segment is generated. Event windows are
  relative to now, so the real recipient list is computed again at approval.
- **Edits replace one channel at a time** and are validated together with the
  other channels' current content, through the same validator as generated
  content. A marketer edit sets `human_edited` and bumps `version`; regenerating
  resets `human_edited`.
- **Approval always wins a race.** Every pre-approval write is guarded in SQL
  (`WHERE status IN (...)`, or a row lock for edits), so a generation or edit
  that finishes after approval changes nothing and returns a conflict. The
  service checks status first only to give a clear message.
- **Approval re-validates against today's audience.** Lengths were measured
  when the content was written or edited; names and cities in the audience
  may be longer now. Approval measures again and refuses (`422`, same
  problems format as an edit) rather than send a message over the limit.
- **Approval approves exactly the validated content.** The service passes the
  content versions it validated; the store locks the campaign row and refuses
  (`409`) if any version changed, so an edit that lands in between is never
  sent unchecked.
- **Approve and enqueue are one transaction,** and the campaign goes straight
  to `sending` with `approved_at` set. A campaign is never `sending` without
  its messages, and messages are never queued for content that can still
  change. `approved` stays in the schema but is not used as a resting state.
- **Recipients are computed at approval** with `INSERT … SELECT` per channel
  from `segment.RecipientsSQL` (rule AND that channel's consent, only the
  campaign's channels), `ON CONFLICT DO NOTHING` on
  `UNIQUE(campaign_id, user_id, channel)`. The rows are created in Postgres,
  not loaded into Go. If nobody is reachable any more, approval is refused
  instead of starting an empty campaign.
- **The service depends on a small `Store` interface.** Flow rules are
  unit-tested with an in-memory store (`make test`, no database); the Postgres
  store is covered by integration tests against the seeded database.

## HTTP API

- **Creating a campaign also generates its segment,** in one request: the
  marketer always wants the audience next, and a segment call is short. If the
  model fails, the campaign is still created (`201`, status `draft`,
  `last_error`), so nothing typed is lost and `POST /campaigns/{id}/segment`
  retries.
- **Content generation is a POST that streams SSE.** It changes state, so it is
  not a GET (`EventSource` only does GET; the frontend reads the stream with
  `fetch`). The stream starts with the first token: until then the handler can
  still answer `404`/`409` as plain JSON, so the client checks the status first
  and treats `200` as a stream. Errors after that arrive as an `error` event
  carrying the status it would have had. If the client disconnects, the request
  context cancels the model call; the attempt is still recorded.
- **Errors are mapped once** (`errorBody`): not found 404, bad input 400,
  wrong status 409, invalid content or model output 422 (with `problems`),
  model unreachable 502. Unexpected errors are logged and returned as a generic
  500 without internal details. LLM failures have their own type
  (`llm.CallError`) so they are not mistaken for database errors.
- **Request bodies are decoded strictly** (unknown fields → 400, 64 KB max), so
  a client cannot send a `segment_rule` or `human_edited` flag and have it
  silently ignored. The segment rule never comes from the client.
- **The campaign exposes the numbers its content is validated with**
  (`variable_lengths`, `limits`), measured at read time like an edit is, so the
  UI's live counter and the server cannot disagree. The UI does not need to know
  the placeholder minimum or the unknown-length fallback.
- **The mock LLM streams with a 40 ms pause per chunk** in the running app, so
  streaming is visible in a demo; tests use no delay.

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
- **Response classes:** 2xx sent; 429, 503, 5xx, 408, timeouts and connection
  errors retryable; every other status permanent. `Retry-After` is read on 429
  and 503, as seconds or an HTTP date. A 409 (key reused with a different
  payload) is permanent: it means our rendering changed between attempts, and
  resending under a new key could deliver twice.
- **The provider client never returns a Go error,** only a `Result` with an
  outcome, status, provider message id, `Retry-After` and latency. The worker
  decides in one place what happens next, and every result maps to one
  `message_attempts` row.
- **Rendered messages are checked before sending.** Approval validated the
  worst case, but a recipient's data can change afterwards; a rendered message
  over a limit (or a recipient without an address) fails permanently without
  a provider call, rather than being cut off or bounced.
- **A 10 s timeout per provider call.** Above the mock's 3 s slow responses;
  a timed-out call is retried with the same key, so a slow accept is replayed,
  not duplicated.
- **Crash recovery:** a claimed message gets a 30 s lease (`lease_until`); if the
  worker dies, messages stuck in `processing` are reclaimed after the lease
  expires.
- **`message_attempts`** records every provider call (status code, error,
  latency) for metrics and debugging.

## Mock provider

- **Behaves like a real provider, with tunable failure rates** (`MOCK_*`
  variables, defaults: 3% random 429, 5% random 500/503, 1% lost responses,
  20–150 ms latency, 1% slow 3 s responses).
- **Real rate limit, not just random 429s:** a per-channel token bucket (100/s,
  burst 100). Every 429 carries `Retry-After`, so the worker's limiter and
  `Retry-After` handling are tested against actual pressure.
- **Idempotency key required.** A repeated key with the same payload returns
  the original result (`Idempotent-Replayed: true`); with a different payload it
  returns 409. Replays are answered before rate limiting and failure injection,
  as providers that cache the first response do.
- **Lost responses:** the message is accepted but the client gets a 500. This is
  the case that makes a provider-side idempotency key necessary; a retry with
  the same key is replayed, not delivered twice.
- **Duplicate-delivery counter:** `/stats` counts identical recipient + content
  accepted under different keys. It must stay 0; it is the end-to-end evidence
  for "nobody receives the same message twice".
- **Validates like a provider:** E.164 phone, email format, SMS ≤ 160
  characters (counted as characters, not bytes), subject ≤ 80. A sender bug
  surfaces as a permanent 400.
- **In-memory state.** Restarting the provider forgets keys; acceptable for a
  mock, and `POST /reset` clears it for demos and tests.

## Observability

- **`/metrics` in Prometheus text format**, `/health` checks the DB connection.
- **`/health` returns 503 when Postgres is unreachable** (2 s ping timeout), not
  just "process is up": an API that cannot reach its database cannot do any
  useful work. The pool reconnects on its own, so health recovers without a
  restart. Compose uses it as the API healthcheck, and the frontend waits for it.

## Schema

- **`text` + `CHECK` instead of Postgres enums** for statuses, channels and event
  types: easier to change in later migrations, same safety.
- **Invariants are also enforced in the database**, as a backstop to the Go code:
  unique (campaign, user, channel) per message, SMS body ≤ 160 and email subject
  ≤ 80 characters on the raw template, SMS has no subject, `processing` requires
  a lease, `sent` requires `sent_at`, approved campaigns require `approved_at`.
  The worst-case length after variable expansion is checked in Go, because it
  depends on the audience.
- **`user_events` table instead of summary columns on `users`.** Rules like "added
  to cart in the last 30 days but did not purchase" need event times; the
  segment compiler emits `EXISTS` / `NOT EXISTS` subqueries served by an index on
  `(user_id, type, occurred_at)`.
- **`campaign_contents` holds only the current content per channel**
  (`UNIQUE(campaign_id, channel)`, `version` counter); every generated version is
  kept in `llm_generations`.
- **Messages are not stored rendered.** Content is locked after approval and
  rendering is deterministic, so the worker renders at send time and retries send
  the same payload.
- **Partial indexes for the worker**: due messages per channel
  (`status IN ('queued','retry')`) and expired leases (`status = 'processing'`)
  stay small no matter how many messages have been sent.

## Seed data

- **Deterministic generator, separate from loading.** `internal/seed` is pure
  (fixed random seed → same users and events) and unit-tested without a
  database; the loader just bulk-inserts with `COPY`.
- **Timestamps are relative to the time of seeding**, so rules like "last 30
  days" keep matching whenever the stack starts. Only the absolute times differ
  between runs, not the shape of the data.
- **Seeding is idempotent.** The seed job runs on every `docker compose up`; it
  takes a table lock and does nothing if users already exist.
- **Shape:** 5,000 users, about 80% Turkish / 20% English, cities weighted
  towards İstanbul, independent email (70%) and SMS (55%) consent so all four
  combinations occur, about 25% inactive users. Users browse in sessions; some
  sessions add to cart and fewer than half of carts convert, which produces
  realistic abandoned carts. With the default seed the case's example brief
  matches 230 users (before consent filtering).
- **Names keep Turkish diacritics** and include compound names ("Muhammed
  Emin"), so worst-case `{{first_name}}` length checks and Turkish text handling
  are exercised. Emails are ASCII-folded and use `example.com`.

## Setup

- **One backend image, four binaries.** `backend/Dockerfile` builds everything
  under `cmd/`; each compose service picks its binary with `command`. One build,
  one cached image.
- **Migrations run with the `migrate/migrate` image** as a one-shot compose
  service; `seed`, `api` and `worker` wait for it to complete successfully.
  Plain SQL files, no custom migration code.
- **Postgres healthcheck uses TCP** (`pg_isready -h 127.0.0.1`). On first boot
  the image's init server listens only on a Unix socket, so a socket check
  reports healthy before the real server accepts connections.
- **Compose works without a `.env` file**; every variable has a mock-mode
  default.

## Repository

- **Default branch `main`.**
