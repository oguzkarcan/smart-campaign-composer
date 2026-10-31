ALTER TABLE llm_generations DROP COLUMN language;
ALTER TABLE campaigns DROP COLUMN audience_languages;
ALTER TABLE messages DROP COLUMN language;

-- Keep one message per channel: Turkish where both exist.
DELETE FROM campaign_contents en USING campaign_contents tr
WHERE en.campaign_id = tr.campaign_id AND en.channel = tr.channel
  AND en.language = 'en' AND tr.language = 'tr';
ALTER TABLE campaign_contents DROP CONSTRAINT campaign_contents_campaign_language_channel_key;
ALTER TABLE campaign_contents ADD CONSTRAINT campaign_contents_campaign_id_channel_key UNIQUE (campaign_id, channel);
ALTER TABLE campaign_contents DROP COLUMN language;
