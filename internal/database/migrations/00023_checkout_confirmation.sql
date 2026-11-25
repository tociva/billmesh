-- +goose Up
ALTER TABLE payments
  ADD COLUMN checkout_confirmation_hash text,
  ADD COLUMN checkout_confirmed_at timestamptz;

ALTER TABLE payments
  ADD CONSTRAINT payments_checkout_confirmation_pair
    CHECK ((checkout_confirmation_hash IS NULL) = (checkout_confirmed_at IS NULL)),
  ADD CONSTRAINT payments_checkout_confirmation_hash_format
    CHECK (checkout_confirmation_hash IS NULL OR checkout_confirmation_hash ~ '^[0-9a-f]{64}$');

-- +goose Down
ALTER TABLE payments
  DROP CONSTRAINT payments_checkout_confirmation_hash_format,
  DROP CONSTRAINT payments_checkout_confirmation_pair,
  DROP COLUMN checkout_confirmed_at,
  DROP COLUMN checkout_confirmation_hash;
