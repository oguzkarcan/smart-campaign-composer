-- Snapshot of how many matched users each channel can reach (consent applied),
-- taken when the segment is generated. audience_count is the matched total.
ALTER TABLE campaigns
    ADD COLUMN audience_sms   integer CHECK (audience_sms IS NULL OR audience_sms >= 0),
    ADD COLUMN audience_email integer CHECK (audience_email IS NULL OR audience_email >= 0);
