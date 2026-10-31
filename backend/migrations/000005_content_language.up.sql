-- Content per recipient language. A campaign has one message per channel for
-- each language (tr, en) in its reachable audience, and every queued message
-- records its recipient's language, so the worker sends the matching one.
ALTER TABLE campaign_contents
    ADD COLUMN language text NOT NULL DEFAULT 'tr' CHECK (language IN ('tr', 'en'));
ALTER TABLE campaign_contents ALTER COLUMN language DROP DEFAULT;
ALTER TABLE campaign_contents DROP CONSTRAINT campaign_contents_campaign_id_channel_key;
ALTER TABLE campaign_contents ADD CONSTRAINT campaign_contents_campaign_language_channel_key
    UNIQUE (campaign_id, language, channel);
-- Content written before this migration was one message for everybody: keep
-- that behaviour by giving it to both languages.
INSERT INTO campaign_contents (campaign_id, channel, language, subject, body, human_edited, version, created_at, updated_at)
SELECT campaign_id, channel, 'en', subject, body, human_edited, version, created_at, updated_at
FROM campaign_contents;

-- The recipient's language when the message was queued; recipients are fixed
-- at approval, like their content.
ALTER TABLE messages ADD COLUMN language text CHECK (language IN ('tr', 'en'));
UPDATE messages m SET language = u.language FROM users u WHERE u.id = m.user_id;
ALTER TABLE messages ALTER COLUMN language SET NOT NULL;

-- Snapshot of the audience counts per language, taken with the other counts
-- when the segment is generated: {"tr": {"matched", "sms", "email"}, ...}.
ALTER TABLE campaigns ADD COLUMN audience_languages jsonb;

-- Which language a content generation was for (NULL for segments).
ALTER TABLE llm_generations ADD COLUMN language text;
