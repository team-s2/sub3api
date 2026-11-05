-- API keys may route to additional groups by requested model.
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS extra_group_ids JSONB;
