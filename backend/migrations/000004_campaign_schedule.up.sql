-- Scheduled sending: an approved campaign waits in 'scheduled' until
-- scheduled_at, then the worker moves it to 'sending'. Its messages are
-- queued at approval with next_attempt_at = scheduled_at, so none is claimed
-- early.
ALTER TABLE campaigns ADD COLUMN scheduled_at timestamptz;

ALTER TABLE campaigns DROP CONSTRAINT campaigns_status_check;
ALTER TABLE campaigns ADD CONSTRAINT campaigns_status_check CHECK (status IN (
    'draft', 'audience_ready', 'content_ready', 'approved', 'scheduled',
    'sending', 'completed', 'failed'));

ALTER TABLE campaigns DROP CONSTRAINT campaigns_check;
ALTER TABLE campaigns ADD CONSTRAINT campaigns_check
    CHECK (status NOT IN ('approved', 'scheduled', 'sending', 'completed') OR approved_at IS NOT NULL);
ALTER TABLE campaigns ADD CONSTRAINT campaigns_scheduled_check
    CHECK (status <> 'scheduled' OR scheduled_at IS NOT NULL);

-- Worker: scheduled campaigns that are due.
CREATE INDEX campaigns_scheduled_idx ON campaigns (scheduled_at) WHERE status = 'scheduled';
