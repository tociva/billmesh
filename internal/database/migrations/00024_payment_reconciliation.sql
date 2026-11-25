-- +goose Up
ALTER TABLE payments
  ADD COLUMN reconciliation_attempts integer NOT NULL DEFAULT 0 CHECK (reconciliation_attempts >= 0),
  ADD COLUMN reconciliation_outcome text,
  ADD COLUMN reconciliation_next_attempt_at timestamptz,
  ADD COLUMN reconciliation_error_code text,
  ADD COLUMN reconciliation_provider_payment_id text,
  ADD COLUMN finance_reconciled_at timestamptz,
  ADD COLUMN finance_reconciliation_outcome text;

DROP INDEX payments_reconciliation_idx;
CREATE INDEX payments_reconciliation_idx
  ON payments (COALESCE(reconciliation_next_attempt_at, last_reconciled_at, created_at), id)
  WHERE provider = 'razorpay' AND provider_order_id IS NOT NULL
    AND status IN ('created', 'authorized')
    AND reconciliation_outcome IS DISTINCT FROM 'attention_required';

CREATE INDEX payments_finance_reconciliation_idx
  ON payments (COALESCE(finance_reconciled_at, updated_at, created_at), id)
  WHERE provider = 'razorpay' AND status IN ('captured', 'refunded');

CREATE TABLE reconciliation_attempts (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  payment_id uuid NOT NULL REFERENCES payments(id),
  outcome text NOT NULL CHECK (outcome IN (
    'in_progress', 'no_capture', 'recovered', 'already_fulfilled',
    'retry_scheduled', 'attention_required', 'finance_matched', 'finance_mismatch'
  )),
  error_code text,
  provider_order_id text,
  provider_payment_id text,
  attempted_at timestamptz NOT NULL DEFAULT now(),
  completed_at timestamptz
);

CREATE INDEX reconciliation_attempts_payment_idx
  ON reconciliation_attempts(payment_id, attempted_at DESC);

CREATE TABLE reconciliation_issues (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  provider text NOT NULL CHECK (provider = 'razorpay'),
  provider_environment text NOT NULL,
  account_id uuid REFERENCES billing_accounts(id),
  payment_id uuid REFERENCES payments(id),
  issue_type text NOT NULL CHECK (issue_type IN (
    'provider_authentication', 'provider_order_missing', 'provider_response_invalid',
    'orphan_order', 'captured_payment_missing', 'capture_mismatch',
    'invoice_mismatch', 'ledger_mismatch', 'refund_mismatch'
  )),
  status text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'acknowledged', 'resolved')),
  provider_order_id text,
  provider_payment_id text,
  provider_refund_id text,
  summary text NOT NULL CHECK (length(summary) BETWEEN 1 AND 500),
  details jsonb NOT NULL DEFAULT '{}',
  first_detected_at timestamptz NOT NULL DEFAULT now(),
  last_detected_at timestamptz NOT NULL DEFAULT now(),
  acknowledged_at timestamptz,
  resolved_at timestamptz,
  resolution_note text,
  version integer NOT NULL DEFAULT 1 CHECK (version > 0)
);

CREATE UNIQUE INDEX reconciliation_issues_identity_idx
  ON reconciliation_issues (
    provider,
    provider_environment,
    issue_type,
    COALESCE(payment_id, '00000000-0000-0000-0000-000000000000'::uuid),
    COALESCE(provider_order_id, ''),
    COALESCE(provider_payment_id, ''),
    COALESCE(provider_refund_id, '')
  )
  WHERE status <> 'resolved';

CREATE INDEX reconciliation_issues_status_idx
  ON reconciliation_issues(status, last_detected_at DESC, id);

CREATE TABLE provider_reconciliation_checkpoints (
  provider text NOT NULL CHECK (provider = 'razorpay'),
  provider_environment text NOT NULL,
  last_inventory_at timestamptz,
  last_inventory_outcome text,
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (provider, provider_environment)
);

-- +goose Down
DROP TABLE provider_reconciliation_checkpoints;
DROP INDEX reconciliation_issues_status_idx;
DROP INDEX reconciliation_issues_identity_idx;
DROP TABLE reconciliation_issues;
DROP INDEX reconciliation_attempts_payment_idx;
DROP TABLE reconciliation_attempts;
DROP INDEX payments_finance_reconciliation_idx;
DROP INDEX payments_reconciliation_idx;
CREATE INDEX payments_reconciliation_idx
  ON payments ((COALESCE(last_reconciled_at, created_at)))
  WHERE provider = 'razorpay' AND provider_order_id IS NOT NULL
    AND status IN ('created', 'authorized');
ALTER TABLE payments
  DROP COLUMN finance_reconciliation_outcome,
  DROP COLUMN finance_reconciled_at,
  DROP COLUMN reconciliation_provider_payment_id,
  DROP COLUMN reconciliation_error_code,
  DROP COLUMN reconciliation_next_attempt_at,
  DROP COLUMN reconciliation_outcome,
  DROP COLUMN reconciliation_attempts;
