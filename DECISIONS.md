# Decisions

Short notes on important decisions and why they were made. Newest decisions are
appended at the bottom of each section. Known gaps and unfinished scope are
listed in the [README](README.md#unfinished-scope-and-known-gaps).

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
- **Prompts are versioned** (`segment-v1`, `content-v2`); the version is stored
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
  brief: no discount is mentioned unless the brief has one. The case's
  example brief gives the same rule as Claude would. A separate `Scripted`
  client drives repair and failure paths in tests.
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
  is replaced by the longest value among the target audience's readers of that
  language. SMS ≤ 160 characters,
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
  `content_ready` → (`scheduled` →) `sending` → `completed`/`failed`. A failed
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
  matches who consented to at least one campaign channel, per language.
  Measuring over both channels at once is slightly conservative for a single
  channel, and keeps one set of numbers per language. Edits are re-measured against the audience as it
  is at edit time, and approval measures again.
- **Audience counts are a snapshot** (`audience_count`, `audience_sms`,
  `audience_email`, and per language `audience_languages`) taken when the
  segment is generated. Event windows are
  relative to now, so the real recipient list is computed again at approval.
- **Edits replace one language's message for one channel at a time** and are
  validated together with that language's other channels, through the same validator as generated
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
  to `sending` (or `scheduled`, see below) with `approved_at` set. A campaign is never `sending` without
  its messages, and messages are never queued for content that can still
  change. `approved` stays in the schema but is not used as a resting state.
- **Recipients are computed at approval** with `INSERT … SELECT` per channel
  from `segment.RecipientsSQL` (rule AND that channel's consent, only the
  campaign's channels, only languages that have content; each message keeps
  the recipient's language), `ON CONFLICT DO NOTHING` on
  `UNIQUE(campaign_id, user_id, channel)`. The rows are created in Postgres,
  not loaded into Go. If nobody is reachable any more, approval is refused
  instead of starting an empty campaign.
- **Scheduled sending reuses the queue's due time.** `send_at` on approval
  (optional, RFC 3339, in the future and at most 30 days ahead) queues the
  messages with `next_attempt_at = send_at` and sets the campaign to
  `scheduled` with `scheduled_at`. The worker already claims only due
  messages, so nothing is sent early without a new code path in the claim.
  Its housekeeping loop moves due `scheduled` campaigns to `sending` before
  the completion check, which only looks at `sending`, so a scheduled
  campaign can never be closed early, and one whose messages all went out in
  the second before its start is closed in the same round.
- **A scheduled campaign is approved like any other:** content validated and
  locked, recipients computed and consent applied at approval. The marketer
  sees the exact count that will be sent, and the worker's send-time checks
  (rendered length, header injection) still apply. The cost: someone who
  joins the segment or withdraws consent before the send time is not picked
  up (listed as a known gap, together with pausing a sending campaign; a
  status gate in the claim would be the place for both).
- **Cancelling a schedule goes back to `content_ready`, not `draft`.** The
  marketer cancels to change something; at `content_ready` the audience and
  messages are kept, editable, and approvable again, while `draft` would
  force regenerating both. In one transaction the campaign row is locked
  (serializing with the worker's start and a concurrent send-now), the
  campaign must still be `scheduled` with its time not yet come, and its
  messages are deleted; if any of them was already claimed (it could be at
  the provider) the transaction rolls back with a `409`. Approving again
  computes the recipients afresh.
- **"Send now" also makes the queued messages due now.** They were queued
  due at `scheduled_at`, so flipping the status alone would leave them
  waiting. The status change is guarded (`WHERE status = 'scheduled'`), so
  it and the worker's own start cannot both win; the loser gets a `409` and
  the UI reloads the campaign. `scheduled_at` is set to the moment sending
  started, which is what the dashboard measures elapsed time from.
- **Times are absolute.** The API takes RFC 3339 with an offset; the UI's
  date-time picker is in the browser's local time and is converted to UTC
  before sending, and shows the time back in local time.
- **Content per recipient language.** Each user has a `language` (`tr`,
  `en`); a campaign gets one message set per language in its reachable
  audience, and each recipient receives their own language. Which languages
  are needed is measured, not guessed from the brief: the audience is
  counted per language, and the variable lengths are measured per language
  (`MaxLengths` is grouped by `u.language`), so English messages are checked
  against English readers' names, not the longest Turkish one.
- **One model call per language, not one call for all.** Each call reuses
  the same prompt, validator and repair loop; a language's lengths and its
  repair rounds stay separate, so a too-long English SMS does not make
  Claude rewrite the Turkish one. The prompt (`content-v2`) names the target
  language and says the brief may be in another one. Languages are written
  one after another and streamed with their language; content is saved only
  if every language succeeds, so a campaign never has half its languages.
  Every attempt is stored with its `language`. Two calls cost more than one
  combined answer; for two short message sets the simpler loop wins.
- **Every message keeps its recipient's language** (`messages.language`,
  set at approval like the recipient list). The worker joins content on
  `(campaign, channel, language)`. Enqueueing only selects recipients whose
  language has content, so a message can never be claimed with nothing to
  send; the service checks first and refuses approval (`409`) with a clear
  message if today's audience includes a language without messages
  (readers joined after the content was written). Approval problems carry
  the language in the path (`en.sms.body`).
- **Edits target one language and channel**
  (`PUT /campaigns/{id}/content/{language}/{channel}`) and are validated
  with that language's lengths.
- **Migration keeps old campaigns working:** content written before
  per-language content was one message for everybody, so it is copied to
  both languages, and queued messages get their recipient's language.
- **The matching-people list is read-only and reports consent instead of
  filtering on it** (`GET /campaigns/{id}/recipients`). Audience membership
  stays defined by the segment rule alone, so there is no add/remove; the
  list exists to answer "who exactly is this?" and to preview real messages.
  Showing users without consent as "skipped" on a channel explains the gap
  between matching users and recipients. It runs the same compiled,
  parameterized rule as counting and enqueueing, live at read time like the
  rest of the page. It pages with previous / next buttons (offset paging,
  fine for a few hundred matches; a very large audience would want keyset
  paging like `GET /campaigns`) and sorts in the database, so the order
  holds across pages: `sort` is a whitelisted key mapped to SQL in the
  store (never interpolated from the request), `order` is asc/desc, and
  ties are broken by id so pages never overlap or skip anyone. Names and
  cities sort with Postgres' ICU Turkish collation (`tr-TR-x-icu`): the
  database's default `en_US` order puts Ç, İ, Ö, Ş and Ü after Z.
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
- **`/health` names the model** (`llm_provider`, `llm_model`) so the header
  can show whether Claude or the mock is writing. It is configuration rather
  than a check, and it does not affect the status code; the UI already polls
  `/health`, so no extra endpoint was needed.
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
  over a limit, an email subject that renders blank or with a line break
  (header injection through recipient data), or a recipient without an
  address fails permanently without a provider call, rather than being cut
  off or bounced.
- **A 10 s timeout per provider call.** Above the mock's 3 s slow responses;
  a timed-out call is retried with the same key, so a slow accept is replayed,
  not duplicated.
- **Crash recovery:** a claimed message gets a 30 s lease (`lease_until`); if the
  worker dies, messages stuck in `processing` are reclaimed after the lease
  expires.
- **Claiming is one statement:** `UPDATE … FROM (SELECT … FOR UPDATE SKIP
  LOCKED LIMIT n)` marks the messages `processing`, sets the lease, counts the
  attempt and returns each message with its recipient and the locked content,
  so sending needs no further queries. Due times and leases use the database
  clock (`now()`).
- **The attempt is counted when it is claimed,** not when it finishes, so a
  worker that dies mid-send still uses up an attempt and no message is retried
  forever. On graceful shutdown, claimed messages that were never sent are
  released and get their attempt back.
- **A result is applied only by the worker that still holds the message**
  (`status = 'processing' AND attempts = <its attempt>`). If the lease expired
  and the message was reclaimed, the late call is still recorded in
  `message_attempts` but does not overwrite the newer attempt; a late "sent"
  is harmless because the retry reuses the idempotency key and is replayed.
  Lease recovery writes no attempt row for the same reason.
- **Backoff uses "equal jitter":** a random wait between half and all of
  `min(1 s · 2^(attempt-1), 60 s)`. Jitter spreads out messages that failed
  together; keeping the lower half keeps the wait growing. On 429/503 the
  wait is the longer of the backoff and `Retry-After`, the latter capped at
  5 minutes so a broken header cannot stall a campaign. 429s count as
  attempts like any other failure; the worker's own limiter keeps them rare.
- **Rate limit: a token bucket per channel, 100/s with a burst of 10,** below
  the provider's burst so the edges of a second do not trip its limit. The
  worker claims only as many messages as it has free send slots (32 per
  channel), so a claimed message waits at most a third of a second for a
  token, far inside its 30 s lease.
- **Graceful shutdown:** on SIGTERM the worker stops claiming, releases
  claimed but unsent messages, and lets calls in flight finish and be
  recorded (bounded by the 10 s client timeout; compose gives it 15 s).
  Cancelling a call mid-flight would leave its outcome unknown and cost an
  attempt; anything left over is recovered by the lease.
- **Campaign completion:** a housekeeping loop (every second) recovers expired
  leases and closes `sending` campaigns with nothing left to send:
  `completed` if at least one message was sent, `failed` if none was. Failed
  messages are visible per message (`last_error`) and in the dashboard.
- **Integration tests run one package at a time and pause the compose
  worker,** since they share the database and claims take due messages of any
  campaign.
- **`message_attempts`** records every provider call (status code, error,
  latency) for metrics and debugging.

## Frontend

- **Same-origin `/api` instead of CORS.** The browser only talks to the
  frontend's origin; the Vite dev server and nginx in the Docker image both
  forward `/api/*` to the API with the prefix stripped. The API stays free of
  CORS configuration, and the same build runs anywhere the proxy points.
- **nginx proxies SSE unbuffered** (`proxy_buffering off`, 1 h read timeout),
  so streamed tokens and progress events reach the browser as they are
  written. The API also sends `X-Accel-Buffering: no`.
- **Content streaming uses `fetch` with a small SSE reader;** the live
  dashboard uses `EventSource`. `EventSource` only does GET, and the content
  endpoint is a POST whose early errors are plain JSON responses, so `fetch`
  is needed to see the status first. The progress stream is a GET, where
  `EventSource`'s automatic reconnect is worth having; the client closes it
  on the `done` event so it does not reconnect to a finished campaign.
- **Tailwind v4 via its Vite plugin,** no PostCSS or Tailwind config files.
- **Visual identity "Sirius Studio"** (Day 7): Plus Jakarta Sans and
  JetBrains Mono from Google Fonts (system fonts if they cannot load), a
  sticky translucent header, numbered section cards (`SectionShell`) that
  match the step bar, and split layouts on wide screens: rule and counts
  beside the matching-people list, each message editor beside its preview.
  Dark mode still comes from remapping the palette, so new components need
  no `dark:` classes. The only exception is the brand mark, whose colours are
  fixed so it looks the same in both themes. Consent in the people table is
  shown as icons, with the words kept for screen readers and on hover, so
  the table fits beside the rule.
- **The segment rule is shown as readable sentences** ("Added to cart at
  least 2 times in the last 7 days"), built by a pure, unit-tested function
  from the same DSL the backend validates, so the marketer sees exactly what
  will be queried, not a model's paraphrase. The raw JSON is one click away.
  The rule is read-only, as required; changing it means editing the brief or
  regenerating.
- **Audience counts show only the campaign's channels,** each with the
  consented count, and a zero count is flagged, since content generation is
  refused when nobody can be reached.
- **Regenerating the audience asks for confirmation when content exists,**
  because the backend discards content written for the previous audience.
- **Content validation is mirrored in the browser** (`src/lib/content.ts`,
  unit-tested against the same cases as the server): same variable rules,
  same code-point counting, and the worst case uses the `variable_lengths` the
  API returns, so the counter shows the number the server will check. The
  server stays authoritative: edits and approval are validated again there,
  and its problems are shown under the matching field. Unlike the server, the
  browser keeps measuring after a broken variable, so the marketer sees every
  problem at once.
- **Messages appear field by field while they are generated.** A small
  forgiving scanner (`src/lib/streamExtract.ts`) reads `sms.body`,
  `email.subject` and `email.body` out of the cut-off JSON, decoding escapes
  and holding back an escape or surrogate pair cut in half, and the same
  editors and SMS / email previews show the text read-only as it grows (a
  variable still being written is held back from the preview, and length
  problems wait for the finished answer). The deltas already carry their
  language, so the editor switches to the language being written; a new
  language or repair attempt starts a fresh answer. The server's strict parse
  stays the only judge: when `done` arrives the validated content replaces
  the live view. The raw output is still one click away.
- **Approval is blocked while an editor has unsaved or invalid changes,** so
  what is approved is exactly what the server has stored and validated; the
  confirm dialog shows how many messages per channel will be queued.
- **The dashboard is driven by the API's progress stream only.** Counts,
  status and completion all come from `GET /campaigns/{id}/events`; the page
  computes nothing the server does not report except throughput, which is
  the change in finished messages over a 5 s window of events. The stream is
  closed on `done`; a network drop is shown as "Reconnecting…" while
  `EventSource` retries, and a server error offers a manual reconnect.
- **The campaign list polls instead of streaming.** It refreshes every 3 s
  only while a listed campaign is approved or sending, reloading the rows
  already shown in one request. One SSE stream per row would be heavier than
  the list is worth; the detail page has the live stream.
- **Routes:** `/` lists campaigns, `/new` is the brief form, and
  `/campaigns/:id` holds the whole flow (audience, messages, delivery), with
  the dashboard moved to the top once the campaign is approved.
- **nginx resolves the API per request** (Docker DNS, `resolver
  127.0.0.11`, `proxy_pass` with a variable) and gives up connecting after
  5 s. Resolving once at startup kept a recreated API container's old IP,
  and a stopped API made requests hang for nginx's 60 s default instead of
  failing.
- **The client turns transport failures into one clear message.** Network
  errors and proxy error pages (502/503/504 without the API's JSON body)
  become "Cannot reach the API…", and requests time out (15 s reads, 30 s
  writes, 3 min for calls that wait on the LLM with repairs) so a stuck
  request ends in an error with a retry instead of an endless spinner. The
  content stream has no overall timeout: its progress is visible and it can
  be cancelled. The header's health check shows a banner while the API is
  down.
- **Schedule choice sits next to the approve button** (*Send now* /
  *Schedule for later* with a date-time picker, defaulting to the next full
  hour). It is checked in the browser with the server's range (at least a
  minute ahead, at most 30 days) and checked again at click time, since the
  page may have been open a while. A scheduled campaign shows the dashboard
  with a countdown banner; the list keeps polling scheduled campaigns so
  their row turns to *Sending* on time.
- **Language tabs in the message editor** (Turkish / English, with how
  many messages each language sends and a dot for unsaved changes,
  problems or missing messages). Every language's editors stay mounted and
  only the active tab is shown, so drafts survive switching tabs; approval
  waits until every language is saved and valid. While generating, the
  preview follows the language being written. The audience view shows a
  per-language table next to the channel counts.
- **Interface language without an i18n library.** A typed dictionary per
  language (`src/lib/i18n.ts`), a reactive `locale` and a `t()` lookup with
  `{name}` placeholders. The Turkish dictionary is typed against the
  English one, so a missing translation fails `vue-tsc`. Two languages and
  a few hundred strings did not justify `vue-i18n`. The choice is saved in
  `localStorage` (wrapped in try/catch: blocked storage only means it is
  not remembered); the first visit follows the browser language.
- **Pure helpers take the locale as an argument** (`describeRule`,
  `formatCountdown`, `formatDuration`), so both languages are unit-tested.
  Rule sentences use a word-order template per language: English says
  "Added to cart at least 2 times in the last 7 days", Turkish puts the
  window first, "Son 7 günde en az 2 kez sepete ürün ekledi".
- **Schedule validation returns codes** (`pick`, `tooSoon`, `tooFar`) and
  the component translates them, instead of English strings from the
  helper. Server messages (validation problems, errors) stay as the API
  writes them; translating them would mean keeping two copies of every
  validation rule's wording.
- **Marketer-first campaign page.** A step bar (Audience → Messages →
  Delivery, from the campaign status by a tested pure function) shows where
  the campaign is and switches between its steps. Messages are previewed as
  they arrive: SMS as a phone bubble, email as an inbox card, filled in for
  a sample recipient of that language. Technical detail stays one click
  away instead of in the way: the segment rule JSON and the model's raw
  streamed output are in collapsed `<details>`, and generation shows a
  progress card.
- **Warm light theme on a linen canvas, dark mode unchanged.** The light theme
  uses `#F7F5F0` as its canvas with a coral and amber wash, and `#F4462A` for the
  Insider One badge. Its neutrals are warmed in one place: `@theme` gives the
  `slate-*` variables Tailwind's stone values, rather than rewriting some 200
  classes, and the `.dark` block already sets every one of them. Hex and
  `stone-*` classes are not part of the dark remap, so each has a `dark:` twin
  holding the previous value. Checked by diffing dark screenshots pixel by pixel
  against the previous build. Indigo stays the action colour.
- **Campaign page as a step workspace, Message Studio on one screen.** Only
  the active step shows; a new campaign opens on Audience so the marketer sees
  who matched before any copy is written, one with messages opens on Messages
  and an approved one on Delivery (`defaultStep`). "Continue to Messages" and
  approving move forward. Hidden steps stay mounted
  (`v-show`, not `v-if`): unsaved drafts, a running generation and the live
  delivery stream live in those components, and unmounting would lose or
  cancel them. For the same reason every editor stays mounted while one
  language and channel is shown, and a dot marks unsaved or invalid text
  on the tabs that hide it. The desktop studio has a fixed height tied to the
  viewport: the textarea takes the spare room, the approval bar is always
  visible, and very long text scrolls inside its field or device. Editors
  report their draft to the studio, which owns the single device preview.
  Below the `lg` breakpoint the columns stack and the page scrolls.
- **"Preview as this person"** picks a user from the matching-people table;
  their first name and city fill the SMS bubble and email card (only the two
  allowed variables, as the worker renders), the SMS shows their phone and
  the email their address, the editor switches to their language's tab, and
  a pill shows who is selected with a reset to the sample. If they did not
  consent to a channel, that preview is dimmed with a note that they will
  not get it. The selection is cleared when the audience is regenerated.
- **Dark mode remaps Tailwind's palette** instead of adding `dark:` to every
  class. Tailwind v4 renders colours through `--color-*` variables, so a
  `.dark` block in `style.css` swaps each scale (50 ↔ 950, 100 ↔ 900, …;
  white → slate-900; slate a little stronger so cards, chips and borders
  stay apart). Every view, table, badge, input and preview follows at once,
  and new UI is dark-ready by default. Only two things opt out with
  explicit `dark:` classes: the phone frame and the raw-JSON block stay
  dark. Primary buttons become light-on-dark tints with dark text, which
  keeps their contrast. `color-scheme: dark` makes native controls (date
  picker, checkboxes) dark too. The choice is saved in `localStorage`;
  without one it defaults to dark, and a small inline script in
  `index.html` applies it before the app loads, so there is no light flash.
- **Unsaved message edits are guarded** on route changes and on closing the
  tab, since edits only reach the server on Save.
- **Frontend checks are part of `make test`:** `vue-tsc` type checking and
  Vitest unit tests, with Node in Docker when it is not installed, like Go.

## Testing

- **The end-to-end test checks invariants, not a lucky run.** Failure
  injection is random and the worker is concurrent, so the test does not
  expect a fixed number of retries. It asserts what must always hold: every
  message ends sent or failed, failures other than the rejected recipient used
  all 5 attempts on retryable errors, the provider's duplicate counter is 0,
  `message_attempts` has exactly one row per provider call, the gap after a
  429 is at least `Retry-After`, and the provider's own limiter never fired.
- **The rate limit is checked from the provider's side:** the mock provider
  enforces 100/s per channel, so `rate_limited = 0` proves the worker stays
  under it. Per-second counts from `message_attempts` are checked too, with
  room for the provider's latency, because rows are stamped when a call
  finishes, not when it starts.
- **A permanent failure is injected by wrapping the provider,** not by
  corrupting seed data: one recipient's number is rejected with a 400.
- **Concurrent approvals are tested with real transactions:** five approvals
  race on one campaign; one wins and every recipient is queued once per
  channel.
- **Injection-like DSL input is tested at both layers.** The validator is
  fed SQL in values, list items, ops, events and `match`, case variants and
  homoglyphs of field names, wrong JSON types and absurd nesting; the
  compiler is checked to produce SQL with no quotes, comments or `;` and one
  argument per placeholder for every operator. Either layer alone would
  stop an injection; the tests keep both honest.
- **The rate limiter is tested as a bound, not just a pace:** besides the
  minimum duration, no 50 ms window of sends exceeds burst plus refill, and
  two channels together take about as long as one, which a shared bucket
  could not do. Time-based tests use margins of hundreds of milliseconds
  and were run repeatedly to rule out flakiness.

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
- **Metrics are read from Postgres at scrape time,** not counted in memory.
  Messages, provider calls and LLM attempts are all rows already, so the
  API's `/metrics` also covers the worker's work, survives restarts, and the
  two processes share nothing. Each scrape is a few aggregates; with millions
  of messages these would become counters kept by the worker or a summary
  table. `*_total` series are row counts, so they only drop if campaigns are
  deleted.
- **Metrics:** `scc_messages{channel,status}`, `scc_campaigns{status}`,
  `scc_provider_attempts_total{channel,outcome,code}` (outcome sent / retry /
  permanent, code 2xx / 429 / 4xx / 5xx / none),
  `scc_provider_attempt_duration_seconds` and
  `scc_llm_generation_duration_seconds` (sum and count),
  `scc_llm_generations_total{kind,result}` (valid / invalid / error) and
  `scc_llm_tokens_total{kind,direction}`. Known label combinations are always
  written (as 0), so a series exists before its first event.
- **`message_attempts.outcome` stores the worker's classification.** The
  HTTP status alone cannot tell a timeout (retried) from a message that
  failed rendering (never sent); both have no status. Rows from before the
  column are reported as `outcome="unknown"`.
- **Rendered into a buffer first,** so a failed database read is a clean 500,
  not a truncated scrape that looks like missing series.
- **The live dashboard is an SSE stream that polls the database** once a
  second per open stream and sends `progress` only when a count or the status
  changes (plus a comment every 15 s to keep proxies from closing it). The
  worker is another process, so the API learns about progress from the
  database either way; polling is simpler than LISTEN/NOTIFY and cheap at
  this scale. The stream ends after the `done` event; since `EventSource`
  reconnects on close, the client closes it on `done`.
- **Progress shows the top failure reasons** (failed messages grouped by
  `last_error`, five most common), so a marketer sees why messages failed,
  not just how many.
- **`GET /campaigns` pages by id** (`?limit=50&before=<id>`, newest first): a
  stable cursor that does not skip or repeat rows when new campaigns are
  created between pages, unlike an offset.

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
- **`campaign_contents` holds only the current content per language and
  channel** (`UNIQUE(campaign_id, language, channel)`, `version` counter); every generated version is
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
- **Checked from a fresh clone:** `docker compose up` with no `.env` and a
  no-cache build, then the example brief run through the frontend's `/api`
  proxy to a completed campaign, and `make test` inside the clone.
- **CI runs the Makefile targets, not its own commands** (`make vet`, `make
  test`), so CI and a reviewer's machine check the same thing. The runner
  installs Go (version from `go.mod`) and Node, so the Makefile uses them
  directly instead of Docker; integration tests need the compose stack and
  stay local.

## Repository

- **Default branch `main`.**
