DROP INDEX campaigns_scheduled_idx;
ALTER TABLE campaigns DROP CONSTRAINT campaigns_scheduled_check;

-- Scheduled campaigns start sending; their messages stay due at scheduled_at.
UPDATE campaigns SET status = 'sending' WHERE status = 'scheduled';

ALTER TABLE campaigns DROP CONSTRAINT campaigns_check;
ALTER TABLE campaigns ADD CONSTRAINT campaigns_check
    CHECK (status NOT IN ('approved', 'sending', 'completed') OR approved_at IS NOT NULL);
ALTER TABLE campaigns DROP CONSTRAINT campaigns_status_check;
ALTER TABLE campaigns ADD CONSTRAINT campaigns_status_check CHECK (status IN (
    'draft', 'audience_ready', 'content_ready', 'approved', 'sending', 'completed', 'failed'));

ALTER TABLE campaigns DROP COLUMN scheduled_at;
