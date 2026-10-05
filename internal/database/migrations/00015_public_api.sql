-- +goose Up
ALTER TABLE payments
  ADD COLUMN checkout_expires_at timestamptz,
  ADD COLUMN last_reconciled_at timestamptz;

ALTER TABLE subscription_transitions
  ADD COLUMN entitlement_schema_version bigint NOT NULL DEFAULT 1 CHECK (entitlement_schema_version > 0),
  ADD COLUMN entitlement_schema jsonb NOT NULL DEFAULT '{"fields":[]}'::jsonb CHECK (jsonb_typeof(entitlement_schema) = 'object');

ALTER TABLE subscriptions
  ADD COLUMN entitlement_schema_version bigint NOT NULL DEFAULT 1 CHECK (entitlement_schema_version > 0),
  ADD COLUMN entitlement_schema jsonb NOT NULL DEFAULT '{"fields":[]}'::jsonb CHECK (jsonb_typeof(entitlement_schema) = 'object');

UPDATE subscription_transitions t
SET entitlement_schema_version = p.entitlement_schema_version
  , entitlement_schema = p.entitlement_schema
FROM products p
WHERE p.id = t.product_id;

UPDATE subscriptions s
SET entitlement_schema_version = p.entitlement_schema_version
  , entitlement_schema = p.entitlement_schema
FROM products p
WHERE p.id = s.product_id;

UPDATE payments
SET checkout_expires_at = created_at + interval '30 minutes'
WHERE status IN ('created', 'authorized');

CREATE INDEX payments_reconciliation_idx
  ON payments ((COALESCE(last_reconciled_at, created_at)))
  WHERE provider = 'razorpay' AND status IN ('created', 'authorized');

UPDATE webhook_endpoints SET api_version = '2';
ALTER TABLE webhook_endpoints
  ADD CONSTRAINT webhook_endpoints_public_api_version CHECK (api_version = '2');

UPDATE products
SET billing_policy = jsonb_set(billing_policy, '{checkout,presentation}', '"modal"'::jsonb, false),
    billing_policy_version = billing_policy_version + 1
WHERE billing_policy->'checkout'->>'presentation' = 'provider_hosted';

CREATE TABLE account_ownership_transfers (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id uuid NOT NULL REFERENCES billing_accounts(id) ON DELETE CASCADE,
  product_id uuid NOT NULL REFERENCES products(id) ON DELETE CASCADE,
  current_customer_id uuid NOT NULL REFERENCES billing_customers(id),
  proposed_customer_id uuid NOT NULL REFERENCES billing_customers(id),
  status text NOT NULL CHECK (status IN (
    'authorized', 'requires_paid_transition', 'ineligible',
    'confirmed', 'cancelled', 'expired'
  )),
  decision_code text NOT NULL,
  required_action text NOT NULL CHECK (required_action IN ('none', 'paid_transition', 'reject')),
  idempotency_key text NOT NULL,
  request_hash text NOT NULL,
  base_revision bigint NOT NULL CHECK (base_revision > 0),
  expires_at timestamptz NOT NULL,
  confirmed_at timestamptz,
  cancelled_at timestamptz,
  created_by text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (account_id, product_id, idempotency_key)
);

CREATE UNIQUE INDEX account_ownership_transfers_one_open_idx
  ON account_ownership_transfers(account_id, product_id)
  WHERE status IN ('authorized', 'requires_paid_transition');

CREATE TABLE billing_account_ownership_history (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id uuid NOT NULL REFERENCES billing_accounts(id) ON DELETE CASCADE,
  product_id uuid NOT NULL REFERENCES products(id) ON DELETE CASCADE,
  previous_customer_id uuid NOT NULL REFERENCES billing_customers(id),
  new_customer_id uuid NOT NULL REFERENCES billing_customers(id),
  transfer_id uuid NOT NULL UNIQUE REFERENCES account_ownership_transfers(id),
  actor_subject text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE billing_account_ownership_history;
DROP INDEX account_ownership_transfers_one_open_idx;
DROP TABLE account_ownership_transfers;
ALTER TABLE webhook_endpoints DROP CONSTRAINT webhook_endpoints_public_api_version;
DROP INDEX payments_reconciliation_idx;
ALTER TABLE subscriptions DROP COLUMN entitlement_schema, DROP COLUMN entitlement_schema_version;
ALTER TABLE subscription_transitions DROP COLUMN entitlement_schema, DROP COLUMN entitlement_schema_version;
ALTER TABLE payments DROP COLUMN last_reconciled_at, DROP COLUMN checkout_expires_at;
