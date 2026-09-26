-- +goose Up
CREATE TABLE invoices (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id uuid NOT NULL REFERENCES billing_accounts(id),
  subscription_id uuid REFERENCES subscriptions(id),
  payment_id uuid REFERENCES payments(id),
  billing_operation_ref text NOT NULL UNIQUE,
  invoice_number text NOT NULL UNIQUE,
  status text NOT NULL CHECK (status IN ('draft', 'open', 'paid', 'void', 'refunded')),
  currency char(3) NOT NULL,
  subtotal_minor bigint NOT NULL CHECK (subtotal_minor >= 0),
  tax_minor bigint NOT NULL DEFAULT 0 CHECK (tax_minor >= 0),
  total_minor bigint NOT NULL CHECK (total_minor >= 0),
  metadata jsonb NOT NULL DEFAULT '{}',
  finalized_at timestamptz,
  paid_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK (total_minor = subtotal_minor + tax_minor),
  CHECK (status = 'draft' OR finalized_at IS NOT NULL)
);

CREATE TABLE invoice_lines (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  invoice_id uuid NOT NULL REFERENCES invoices(id) ON DELETE RESTRICT,
  description text NOT NULL,
  quantity bigint NOT NULL CHECK (quantity > 0),
  unit_price_minor bigint NOT NULL CHECK (unit_price_minor >= 0),
  amount_minor bigint NOT NULL CHECK (amount_minor >= 0),
  metadata jsonb NOT NULL DEFAULT '{}',
  created_at timestamptz NOT NULL DEFAULT now(),
  CHECK (amount_minor = quantity * unit_price_minor)
);

CREATE TABLE audit_log (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  actor_subject text NOT NULL,
  actor_type text NOT NULL CHECK (actor_type IN ('user', 'service', 'system')),
  action text NOT NULL,
  resource_type text NOT NULL,
  resource_id text NOT NULL,
  reason text,
  request_id text,
  before_state jsonb,
  after_state jsonb,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX audit_log_resource_idx ON audit_log(resource_type, resource_id, created_at DESC);
CREATE INDEX audit_log_actor_idx ON audit_log(actor_subject, created_at DESC);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION prevent_finalized_invoice_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.finalized_at IS NOT NULL THEN
    RAISE EXCEPTION 'finalized invoices are immutable';
  END IF;
  RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER invoices_prevent_finalized_update
BEFORE UPDATE OR DELETE ON invoices
FOR EACH ROW EXECUTE FUNCTION prevent_finalized_invoice_mutation();

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION prevent_audit_log_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'audit log is append-only';
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER audit_log_prevent_update_delete
BEFORE UPDATE OR DELETE ON audit_log
FOR EACH ROW EXECUTE FUNCTION prevent_audit_log_mutation();

-- +goose Down
DROP TRIGGER audit_log_prevent_update_delete ON audit_log;
DROP FUNCTION prevent_audit_log_mutation();
DROP TRIGGER invoices_prevent_finalized_update ON invoices;
DROP FUNCTION prevent_finalized_invoice_mutation();
DROP TABLE audit_log;
DROP TABLE invoice_lines;
DROP TABLE invoices;
