-- +goose Up
ALTER TABLE billing_accounts ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now();
ALTER TABLE audit_log ADD COLUMN account_id uuid REFERENCES billing_accounts(id);
ALTER TABLE account_links ADD COLUMN environment text NOT NULL DEFAULT 'production';
ALTER TABLE account_links DROP CONSTRAINT account_links_pkey;
ALTER TABLE account_links DROP CONSTRAINT account_links_account_id_application_organization_id_key;
ALTER TABLE account_links ADD PRIMARY KEY (application, organization_id, environment);
ALTER TABLE account_links ADD UNIQUE (account_id, application, organization_id, environment);

ALTER TABLE plans ADD COLUMN billing_interval text NOT NULL DEFAULT 'monthly'
  CHECK (billing_interval IN ('monthly', 'annual'));
ALTER TABLE subscriptions ADD COLUMN price_minor bigint NOT NULL DEFAULT 0 CHECK (price_minor >= 0);
ALTER TABLE subscriptions ADD COLUMN currency char(3) NOT NULL DEFAULT 'INR';
ALTER TABLE subscriptions ADD COLUMN billing_interval text NOT NULL DEFAULT 'monthly'
  CHECK (billing_interval IN ('monthly', 'annual'));
ALTER TABLE subscriptions ADD COLUMN grace_period_end timestamptz;
ALTER TABLE subscriptions ADD COLUMN cancelled_at timestamptz;
ALTER TABLE subscriptions ADD COLUMN product_id uuid REFERENCES products(id);
UPDATE subscriptions s SET product_id=p.product_id FROM plans p WHERE p.id=s.plan_id;
ALTER TABLE subscriptions ALTER COLUMN product_id SET NOT NULL;
CREATE UNIQUE INDEX subscriptions_one_current_product_idx
  ON subscriptions(account_id, product_id)
  WHERE status IN ('pending', 'active', 'past_due');

CREATE TABLE subscription_history (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  subscription_id uuid NOT NULL REFERENCES subscriptions(id),
  status subscription_status NOT NULL,
  plan_id uuid NOT NULL REFERENCES plans(id),
  effective_at timestamptz NOT NULL DEFAULT now(),
  operation_ref text NOT NULL UNIQUE,
  metadata jsonb NOT NULL DEFAULT '{}'
);

CREATE TABLE credit_packs (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  product_id uuid NOT NULL REFERENCES products(id),
  slug text NOT NULL,
  name text NOT NULL,
  credits bigint NOT NULL CHECK (credits > 0),
  price_minor bigint NOT NULL CHECK (price_minor >= 0),
  currency char(3) NOT NULL,
  validity_days integer CHECK (validity_days IS NULL OR validity_days > 0),
  active boolean NOT NULL DEFAULT true,
  UNIQUE(product_id, slug)
);

ALTER TABLE usage_events ADD COLUMN meter text NOT NULL DEFAULT 'workflow.execution';
ALTER TABLE usage_events ADD COLUMN quantity bigint NOT NULL DEFAULT 0 CHECK (quantity >= 0);
ALTER TABLE usage_events ADD COLUMN application text NOT NULL DEFAULT '';
ALTER TABLE usage_events ADD COLUMN metadata jsonb NOT NULL DEFAULT '{}';

ALTER TABLE payments ADD COLUMN credit_pack_id uuid REFERENCES credit_packs(id);
ALTER TABLE payments ADD COLUMN credits bigint CHECK (credits IS NULL OR credits > 0);
ALTER TABLE payments ADD COLUMN operation_ref text UNIQUE;
ALTER TABLE payments ADD COLUMN failure_reason text;

CREATE TABLE refunds (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  payment_id uuid NOT NULL REFERENCES payments(id),
  provider_refund_id text NOT NULL UNIQUE,
  amount_minor bigint NOT NULL CHECK (amount_minor > 0),
  credits_reversed bigint NOT NULL CHECK (credits_reversed >= 0),
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE credit_notes (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  invoice_id uuid NOT NULL REFERENCES invoices(id),
  refund_id uuid NOT NULL UNIQUE REFERENCES refunds(id),
  credit_note_number text NOT NULL UNIQUE,
  currency char(3) NOT NULL,
  amount_minor bigint NOT NULL CHECK (amount_minor > 0),
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE webhook_endpoints (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id uuid NOT NULL REFERENCES billing_accounts(id) ON DELETE CASCADE,
  application text NOT NULL,
  target_url text NOT NULL,
  secret text NOT NULL,
  active boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(account_id, application)
);

CREATE TABLE threshold_notifications (
  wallet_id uuid NOT NULL REFERENCES wallets(id) ON DELETE CASCADE,
  period_key text NOT NULL,
  threshold integer NOT NULL CHECK (threshold > 0 AND threshold <= 100),
  event_id uuid NOT NULL REFERENCES outbox_events(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY(wallet_id, period_key, threshold)
);

CREATE INDEX usage_events_filter_idx ON usage_events(wallet_id, meter, occurred_at);
CREATE INDEX subscriptions_account_status_idx ON subscriptions(account_id, status);

INSERT INTO products(slug, name) VALUES
  ('daybook', 'Daybook'),
  ('taskmesh', 'Taskmesh')
ON CONFLICT(slug) DO NOTHING;

INSERT INTO plans(product_id, slug, name, price_minor, currency, included_credits, entitlements, active, billing_interval)
SELECT id, 'daybook-free', 'Daybook Free', 0, 'INR', 100, '{"workflow_execution":true,"standalone_workflow":false}', true, 'monthly' FROM products WHERE slug='daybook'
ON CONFLICT(product_id,slug) DO NOTHING;
INSERT INTO plans(product_id, slug, name, price_minor, currency, included_credits, entitlements, active, billing_interval)
SELECT id, 'daybook-paid', 'Daybook Paid', 99900, 'INR', 1000, '{"workflow_execution":true,"standalone_workflow":false}', true, 'monthly' FROM products WHERE slug='daybook'
ON CONFLICT(product_id,slug) DO NOTHING;
INSERT INTO plans(product_id, slug, name, price_minor, currency, included_credits, entitlements, active, billing_interval)
SELECT id, 'professional', 'Taskmesh Professional', 149900, 'INR', 1000, '{"workflow_execution":true,"standalone_workflow":true}', true, 'monthly' FROM products WHERE slug='taskmesh'
ON CONFLICT(product_id,slug) DO NOTHING;
INSERT INTO credit_packs(product_id, slug, name, credits, price_minor, currency, validity_days)
SELECT id, 'credits-500', '500 Credits', 500, 49900, 'INR', NULL FROM products WHERE slug='daybook'
ON CONFLICT(product_id,slug) DO NOTHING;

-- +goose Down
DROP INDEX subscriptions_account_status_idx;
DROP INDEX usage_events_filter_idx;
DROP TABLE threshold_notifications;
DROP TABLE webhook_endpoints;
DROP TABLE credit_notes;
DROP TABLE refunds;
ALTER TABLE payments DROP COLUMN failure_reason, DROP COLUMN operation_ref, DROP COLUMN credits, DROP COLUMN credit_pack_id;
ALTER TABLE usage_events DROP COLUMN metadata, DROP COLUMN application, DROP COLUMN quantity, DROP COLUMN meter;
DROP TABLE credit_packs;
DROP TABLE subscription_history;
DROP INDEX subscriptions_one_current_product_idx;
ALTER TABLE subscriptions DROP COLUMN product_id, DROP COLUMN cancelled_at, DROP COLUMN grace_period_end, DROP COLUMN billing_interval, DROP COLUMN currency, DROP COLUMN price_minor;
ALTER TABLE plans DROP COLUMN billing_interval;
ALTER TABLE account_links DROP CONSTRAINT account_links_pkey;
ALTER TABLE account_links DROP CONSTRAINT account_links_account_id_application_organization_id_environment_key;
ALTER TABLE account_links ADD PRIMARY KEY (application, organization_id);
ALTER TABLE account_links ADD UNIQUE (account_id, application, organization_id);
ALTER TABLE account_links DROP COLUMN environment;
ALTER TABLE billing_accounts DROP COLUMN updated_at;
ALTER TABLE audit_log DROP COLUMN account_id;
