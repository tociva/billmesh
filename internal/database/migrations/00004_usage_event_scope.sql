-- +goose Up
ALTER TABLE usage_events DROP CONSTRAINT usage_events_external_id_key;
ALTER TABLE usage_events ADD CONSTRAINT usage_events_wallet_external_id_key UNIQUE(wallet_id, external_id);

-- +goose Down
ALTER TABLE usage_events DROP CONSTRAINT usage_events_wallet_external_id_key;
ALTER TABLE usage_events ADD CONSTRAINT usage_events_external_id_key UNIQUE(external_id);
