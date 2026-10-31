-- How the worker classified each provider call. http_status alone cannot tell
-- a timeout (retried) from a message that failed rendering (never sent);
-- both have no status. NULL only for rows written before this column.
ALTER TABLE message_attempts
    ADD COLUMN outcome text CHECK (outcome IN ('sent', 'retry', 'permanent'));
