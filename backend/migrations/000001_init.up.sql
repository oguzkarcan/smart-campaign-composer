-- Fake recipients. Consent is stored per channel and enforced by the backend
-- on every audience query.
CREATE TABLE users (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    first_name   text        NOT NULL,
    last_name    text        NOT NULL,
    email        text        NOT NULL UNIQUE,
    phone        text        NOT NULL UNIQUE,
    city         text        NOT NULL,
    language     text        NOT NULL CHECK (language IN ('tr', 'en')),
    email_opt_in boolean     NOT NULL DEFAULT false,
    sms_opt_in   boolean     NOT NULL DEFAULT false,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX users_city_idx ON users (city);

-- Behavioural history used by segment rules, e.g. "added to cart in the last
-- 30 days but did not purchase".
CREATE TABLE user_events (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id     bigint        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    type        text          NOT NULL CHECK (type IN ('cart_add', 'purchase', 'page_view')),
    amount      numeric(12,2) CHECK (amount IS NULL OR amount >= 0),
    occurred_at timestamptz   NOT NULL
);

-- Serves the EXISTS / NOT EXISTS subqueries the segment compiler generates.
CREATE INDEX user_events_user_type_time_idx ON user_events (user_id, type, occurred_at);

CREATE TABLE campaigns (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    brief          text        NOT NULL CHECK (char_length(brief) BETWEEN 1 AND 2000),
    status         text        NOT NULL DEFAULT 'draft' CHECK (status IN (
                       'draft', 'audience_ready', 'content_ready', 'approved',
                       'sending', 'completed', 'failed')),
    channels       text[]      NOT NULL DEFAULT '{}'
                       CHECK (channels <@ ARRAY['sms', 'email']::text[]),
    segment_rule   jsonb,
    audience_count integer     CHECK (audience_count IS NULL OR audience_count >= 0),
    last_error     text,
    approved_at    timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    CHECK (status NOT IN ('approved', 'sending', 'completed') OR approved_at IS NOT NULL)
);

-- Current content per channel. Earlier versions live in llm_generations.
-- The length checks are a backstop on the raw template; the backend checks the
-- worst-case length after variable expansion.
CREATE TABLE campaign_contents (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    campaign_id  bigint      NOT NULL REFERENCES campaigns (id) ON DELETE CASCADE,
    channel      text        NOT NULL CHECK (channel IN ('sms', 'email')),
    subject      text,
    body         text        NOT NULL CHECK (char_length(body) > 0),
    human_edited boolean     NOT NULL DEFAULT false,
    version      integer     NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (campaign_id, channel),
    CHECK (channel <> 'sms' OR (subject IS NULL AND char_length(body) <= 160)),
    CHECK (channel <> 'email' OR (subject IS NOT NULL AND char_length(subject) BETWEEN 1 AND 80))
);

-- Audit trail of every LLM call, including failed and repaired attempts.
CREATE TABLE llm_generations (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    campaign_id       bigint      NOT NULL REFERENCES campaigns (id) ON DELETE CASCADE,
    kind              text        NOT NULL CHECK (kind IN ('segment', 'content')),
    model             text        NOT NULL,
    prompt_version    text        NOT NULL,
    attempt           integer     NOT NULL CHECK (attempt >= 1),
    raw_output        text,
    parsed            jsonb,
    validation_errors jsonb,
    valid             boolean     NOT NULL,
    error             text,
    input_tokens      integer,
    output_tokens     integer,
    latency_ms        integer,
    created_at        timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX llm_generations_campaign_idx ON llm_generations (campaign_id, created_at);

-- The send queue. One row per (campaign, user, channel); the UNIQUE constraint
-- makes enqueueing idempotent and id doubles as the provider idempotency key.
CREATE TABLE messages (
    id                  bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    campaign_id         bigint      NOT NULL REFERENCES campaigns (id) ON DELETE CASCADE,
    user_id             bigint      NOT NULL REFERENCES users (id),
    channel             text        NOT NULL CHECK (channel IN ('sms', 'email')),
    status              text        NOT NULL DEFAULT 'queued' CHECK (status IN (
                            'queued', 'processing', 'sent', 'retry', 'failed')),
    attempts            integer     NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at     timestamptz NOT NULL DEFAULT now(),
    lease_until         timestamptz,
    last_error          text,
    provider_message_id text,
    sent_at             timestamptz,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    UNIQUE (campaign_id, user_id, channel),
    CHECK (status <> 'processing' OR lease_until IS NOT NULL),
    CHECK (status <> 'sent' OR sent_at IS NOT NULL)
);

-- Worker claim: next due messages per channel (FOR UPDATE SKIP LOCKED).
CREATE INDEX messages_due_idx ON messages (channel, next_attempt_at)
    WHERE status IN ('queued', 'retry');
-- Lease recovery: processing messages whose worker died.
CREATE INDEX messages_lease_idx ON messages (lease_until)
    WHERE status = 'processing';
-- Dashboard counts per campaign.
CREATE INDEX messages_campaign_status_idx ON messages (campaign_id, status);

-- One row per provider call, for metrics and debugging retries.
CREATE TABLE message_attempts (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    message_id  bigint      NOT NULL REFERENCES messages (id) ON DELETE CASCADE,
    attempt     integer     NOT NULL CHECK (attempt >= 1),
    http_status integer,
    error       text,
    latency_ms  integer     NOT NULL CHECK (latency_ms >= 0),
    created_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (message_id, attempt)
);
