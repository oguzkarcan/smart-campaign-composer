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