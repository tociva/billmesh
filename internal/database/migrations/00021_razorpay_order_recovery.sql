-- +goose Up
ALTER TABLE payments
  ADD COLUMN provider_receipt text,
  ADD COLUMN provider_environment text;

ALTER TABLE payments
  ADD CONSTRAINT payments_provider_receipt_format
    CHECK (provider_receipt IS NULL OR (
      length(provider_receipt) BETWEEN 1 AND 40
      AND provider_receipt ~ '^[ -~]+$'
    )),
  ADD CONSTRAINT payments_provider_environment_required
    CHECK ((provider_receipt IS NULL) = (provider_environment IS NULL));

CREATE UNIQUE INDEX payments_provider_receipt_idx
  ON payments(provider, provider_environment, provider_receipt)
  WHERE provider_receipt IS NOT NULL;

DROP INDEX payments_reconciliation_idx;
CREATE INDEX payments_reconciliation_idx
  ON payments ((COALESCE(last_reconciled_at, created_at)))
  WHERE provider = 'razorpay' AND provider_order_id IS NOT NULL
    AND status IN ('created', 'authorized');

-- +goose Down
DROP INDEX payments_reconciliation_idx;
CREATE INDEX payments_reconciliation_idx
  ON payments ((COALESCE(last_reconciled_at, created_at)))
  WHERE provider = 'razorpay' AND status IN ('created', 'authorized');
DROP INDEX payments_provider_receipt_idx;
ALTER TABLE payments
  DROP CONSTRAINT payments_provider_environment_required,
  DROP CONSTRAINT payments_provider_receipt_format,
  DROP COLUMN provider_environment,
  DROP COLUMN provider_receipt;
