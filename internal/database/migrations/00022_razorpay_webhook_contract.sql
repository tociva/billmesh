-- +goose Up
ALTER TABLE provider_events
  ADD COLUMN raw_payload bytea,
  ADD COLUMN payload_sha256 text,
  ADD COLUMN provider_account_id text;

ALTER TABLE provider_events
  ADD CONSTRAINT provider_events_payload_sha256_format
    CHECK (payload_sha256 IS NULL OR payload_sha256 ~ '^[0-9a-f]{64}$');

-- +goose Down
ALTER TABLE provider_events
  DROP CONSTRAINT provider_events_payload_sha256_format,
  DROP COLUMN provider_account_id,
  DROP COLUMN payload_sha256,
  DROP COLUMN raw_payload;
