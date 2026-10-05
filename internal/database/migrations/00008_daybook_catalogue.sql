-- +goose Up
-- Subscription plans are catalogue configuration and are intentionally not
-- installed by schema migrations.

-- +goose Down
-- No-op: this migration no longer installs catalogue records.
