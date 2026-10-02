-- +goose Up
CREATE TABLE billing_customers (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  issuer text NOT NULL,
  external_subject text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (issuer, external_subject)
);

ALTER TABLE billing_accounts
  ADD COLUMN customer_id uuid REFERENCES billing_customers(id);
CREATE INDEX billing_accounts_customer_idx ON billing_accounts(customer_id);

ALTER TABLE plans
  ADD COLUMN description text NOT NULL DEFAULT '',
  ADD COLUMN billing_model text NOT NULL DEFAULT 'paid'
    CHECK (billing_model IN ('free', 'paid')),
  ADD COLUMN selectable boolean NOT NULL DEFAULT true,
  ADD COLUMN default_for_product boolean NOT NULL DEFAULT false,
  ADD COLUMN checkout_enabled boolean NOT NULL DEFAULT true,
  ADD COLUMN effective_from timestamptz NOT NULL DEFAULT now(),
  ADD COLUMN effective_to timestamptz;

UPDATE plans
SET billing_model = CASE WHEN price_minor = 0 THEN 'free' ELSE 'paid' END,
    effective_from = created_at,
    checkout_enabled = active;

UPDATE plans
SET default_for_product = true
WHERE id IN (
  SELECT DISTINCT ON (product_id) id
  FROM plans
  WHERE active AND billing_model = 'free'
  ORDER BY product_id, price_minor, created_at, id
);

CREATE UNIQUE INDEX plans_one_default_per_product_idx
  ON plans(product_id)
  WHERE default_for_product AND active;
CREATE INDEX plans_catalogue_idx
  ON plans(product_id, active, selectable, effective_from, effective_to);

ALTER TABLE subscriptions
  ADD COLUMN plan_version bigint NOT NULL DEFAULT 1,
  ADD COLUMN plan_name text NOT NULL DEFAULT '',
  ADD COLUMN plan_description text NOT NULL DEFAULT '',
  ADD COLUMN billing_model text NOT NULL DEFAULT 'paid'
    CHECK (billing_model IN ('free', 'paid'));

UPDATE subscriptions s
SET plan_version = p.version,
    plan_name = p.name,
    plan_description = p.description,
    billing_model = p.billing_model
FROM plans p
WHERE p.id = s.plan_id;

CREATE TABLE billing_state_revisions (
  account_id uuid NOT NULL REFERENCES billing_accounts(id) ON DELETE CASCADE,
  product_id uuid NOT NULL REFERENCES products(id) ON DELETE CASCADE,
  revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (account_id, product_id)
);

INSERT INTO billing_state_revisions(account_id, product_id)
SELECT account_id, product_id FROM subscriptions
UNION
SELECT account_id, product_id FROM wallets
ON CONFLICT DO NOTHING;

CREATE TABLE catalogue_revisions (
  product_id uuid PRIMARY KEY REFERENCES products(id) ON DELETE CASCADE,
  revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
  updated_at timestamptz NOT NULL DEFAULT now()
);

INSERT INTO catalogue_revisions(product_id)
SELECT id FROM products
ON CONFLICT DO NOTHING;

CREATE TABLE subscription_transitions (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id uuid NOT NULL REFERENCES billing_accounts(id),
  product_id uuid NOT NULL REFERENCES products(id),
  subscription_id uuid REFERENCES subscriptions(id),
  target_plan_id uuid NOT NULL REFERENCES plans(id),
  target_plan_version bigint NOT NULL CHECK (target_plan_version > 0),
  target_plan_name text NOT NULL,
  target_plan_description text NOT NULL DEFAULT '',
  billing_model text NOT NULL CHECK (billing_model IN ('free', 'paid')),
  price_minor bigint NOT NULL CHECK (price_minor >= 0),
  currency char(3) NOT NULL,
  billing_interval text NOT NULL CHECK (billing_interval IN ('monthly', 'annual')),
  included_credits bigint NOT NULL CHECK (included_credits >= 0),
  entitlements jsonb NOT NULL DEFAULT '{}',
  operation text NOT NULL CHECK (operation IN ('activate', 'upgrade', 'downgrade', 'change', 'reactivate', 'renew')),
  effective text NOT NULL DEFAULT 'immediate' CHECK (effective IN ('immediate', 'period_end')),
  status text NOT NULL CHECK (status IN ('requires_payment', 'processing', 'completed', 'failed', 'expired', 'cancelled')),
  idempotency_key text NOT NULL,
  request_hash text NOT NULL,
  checkout_expires_at timestamptz,
  effective_at timestamptz,
  failure_code text,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (account_id, product_id, idempotency_key)
);
CREATE INDEX subscription_transitions_current_idx
  ON subscription_transitions(account_id, product_id, created_at DESC);
CREATE UNIQUE INDEX subscription_transitions_one_pending_idx
  ON subscription_transitions(account_id, product_id)
  WHERE status IN ('requires_payment', 'processing');

ALTER TABLE payments
  ADD COLUMN purpose text NOT NULL DEFAULT 'credit_pack'
    CHECK (purpose IN ('credit_pack', 'subscription')),
  ADD COLUMN transition_id uuid REFERENCES subscription_transitions(id);
CREATE UNIQUE INDEX payments_transition_idx
  ON payments(transition_id)
  WHERE transition_id IS NOT NULL;

ALTER TABLE webhook_endpoints
  ADD COLUMN api_version text NOT NULL DEFAULT '1',
  ADD COLUMN event_types text[] NOT NULL DEFAULT '{}',
  ADD COLUMN previous_secret text,
  ADD COLUMN previous_secret_expires_at timestamptz;

ALTER TABLE outbox_events
  ADD COLUMN account_id uuid REFERENCES billing_accounts(id),
  ADD COLUMN product_id uuid REFERENCES products(id),
  ADD COLUMN billing_revision bigint,
  ADD COLUMN schema_version text NOT NULL DEFAULT '1';

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION bump_billing_state_revision()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  resolved_account uuid;
  resolved_product uuid;
BEGIN
  resolved_account := CASE WHEN TG_OP = 'DELETE' THEN OLD.account_id ELSE NEW.account_id END;
  resolved_product := CASE WHEN TG_OP = 'DELETE' THEN OLD.product_id ELSE NEW.product_id END;
  INSERT INTO billing_state_revisions(account_id, product_id, revision, updated_at)
  VALUES(resolved_account, resolved_product, 1, now())
  ON CONFLICT(account_id, product_id)
  DO UPDATE SET revision = billing_state_revisions.revision + 1, updated_at = now();
  RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER subscriptions_bump_billing_revision
AFTER INSERT OR UPDATE OR DELETE ON subscriptions
FOR EACH ROW EXECUTE FUNCTION bump_billing_state_revision();

CREATE TRIGGER wallets_bump_billing_revision
AFTER INSERT OR UPDATE OR DELETE ON wallets
FOR EACH ROW EXECUTE FUNCTION bump_billing_state_revision();

CREATE TRIGGER transitions_bump_billing_revision
AFTER INSERT OR UPDATE OR DELETE ON subscription_transitions
FOR EACH ROW EXECUTE FUNCTION bump_billing_state_revision();

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION bump_catalogue_revision()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  resolved_product uuid;
BEGIN
  IF TG_TABLE_NAME = 'products' THEN
    resolved_product := CASE WHEN TG_OP = 'DELETE' THEN OLD.id ELSE NEW.id END;
  ELSE
    resolved_product := CASE WHEN TG_OP = 'DELETE' THEN OLD.product_id ELSE NEW.product_id END;
  END IF;
  INSERT INTO catalogue_revisions(product_id, revision, updated_at)
  VALUES(resolved_product, 1, now())
  ON CONFLICT(product_id)
  DO UPDATE SET revision = catalogue_revisions.revision + 1, updated_at = now();
  RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER products_bump_catalogue_revision
AFTER INSERT OR UPDATE ON products
FOR EACH ROW EXECUTE FUNCTION bump_catalogue_revision();

CREATE TRIGGER plans_bump_catalogue_revision
AFTER INSERT OR UPDATE OR DELETE ON plans
FOR EACH ROW EXECUTE FUNCTION bump_catalogue_revision();

CREATE TRIGGER credit_packs_bump_catalogue_revision
AFTER INSERT OR UPDATE OR DELETE ON credit_packs
FOR EACH ROW EXECUTE FUNCTION bump_catalogue_revision();

-- +goose Down
DROP TRIGGER credit_packs_bump_catalogue_revision ON credit_packs;
DROP TRIGGER plans_bump_catalogue_revision ON plans;
DROP TRIGGER products_bump_catalogue_revision ON products;
DROP FUNCTION bump_catalogue_revision();

DROP TRIGGER transitions_bump_billing_revision ON subscription_transitions;
DROP TRIGGER wallets_bump_billing_revision ON wallets;
DROP TRIGGER subscriptions_bump_billing_revision ON subscriptions;
DROP FUNCTION bump_billing_state_revision();

ALTER TABLE outbox_events
  DROP COLUMN schema_version,
  DROP COLUMN billing_revision,
  DROP COLUMN product_id,
  DROP COLUMN account_id;

ALTER TABLE webhook_endpoints
  DROP COLUMN previous_secret_expires_at,
  DROP COLUMN previous_secret,
  DROP COLUMN event_types,
  DROP COLUMN api_version;

DROP INDEX payments_transition_idx;
ALTER TABLE payments
  DROP COLUMN transition_id,
  DROP COLUMN purpose;

DROP INDEX subscription_transitions_one_pending_idx;
DROP INDEX subscription_transitions_current_idx;
DROP TABLE subscription_transitions;
DROP TABLE catalogue_revisions;
DROP TABLE billing_state_revisions;

ALTER TABLE subscriptions
  DROP COLUMN billing_model,
  DROP COLUMN plan_description,
  DROP COLUMN plan_name,
  DROP COLUMN plan_version;

DROP INDEX plans_catalogue_idx;
DROP INDEX plans_one_default_per_product_idx;
ALTER TABLE plans
  DROP COLUMN effective_to,
  DROP COLUMN effective_from,
  DROP COLUMN checkout_enabled,
  DROP COLUMN default_for_product,
  DROP COLUMN selectable,
  DROP COLUMN billing_model,
  DROP COLUMN description;

DROP INDEX billing_accounts_customer_idx;
ALTER TABLE billing_accounts DROP COLUMN customer_id;
DROP TABLE billing_customers;
