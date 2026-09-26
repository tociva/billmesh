-- +goose Up
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TYPE subscription_status AS ENUM ('pending','active','past_due','cancelled','expired');
CREATE TYPE reservation_status AS ENUM ('reserved','settled','released','expired');
CREATE TYPE payment_status AS ENUM ('created','authorized','captured','failed','refunded');

CREATE TABLE billing_accounts (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  name text NOT NULL,
  external_ref text UNIQUE,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE account_links (
  account_id uuid NOT NULL REFERENCES billing_accounts(id) ON DELETE CASCADE,
  application text NOT NULL,
  organization_id text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (application, organization_id),
  UNIQUE (account_id, application, organization_id)
);
CREATE TABLE products (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), slug text NOT NULL UNIQUE, name text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE plans (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), product_id uuid NOT NULL REFERENCES products(id),
  slug text NOT NULL, name text NOT NULL, price_minor bigint NOT NULL CHECK (price_minor >= 0),
  currency char(3) NOT NULL, included_credits bigint NOT NULL CHECK (included_credits >= 0),
  entitlements jsonb NOT NULL DEFAULT '{}', active boolean NOT NULL DEFAULT true,
  UNIQUE(product_id, slug)
);
CREATE TABLE subscriptions (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), account_id uuid NOT NULL REFERENCES billing_accounts(id),
  plan_id uuid NOT NULL REFERENCES plans(id), status subscription_status NOT NULL,
  current_period_start timestamptz, current_period_end timestamptz, cancel_at_period_end boolean NOT NULL DEFAULT false,
  version bigint NOT NULL DEFAULT 1, created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE wallets (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), account_id uuid NOT NULL REFERENCES billing_accounts(id),
  product_id uuid NOT NULL REFERENCES products(id), available bigint NOT NULL DEFAULT 0 CHECK (available >= 0),
  reserved bigint NOT NULL DEFAULT 0 CHECK (reserved >= 0), created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(account_id, product_id)
);
CREATE TABLE credit_grants (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), wallet_id uuid NOT NULL REFERENCES wallets(id),
  source text NOT NULL, operation_ref text NOT NULL UNIQUE, amount bigint NOT NULL CHECK (amount > 0),
  remaining bigint NOT NULL CHECK (remaining >= 0 AND remaining <= amount), expires_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE reservations (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), wallet_id uuid NOT NULL REFERENCES wallets(id),
  execution_id text NOT NULL, operation_seq integer NOT NULL CHECK (operation_seq >= 0),
  requested bigint NOT NULL CHECK (requested > 0), settled bigint CHECK (settled >= 0),
  status reservation_status NOT NULL DEFAULT 'reserved', expires_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(wallet_id, execution_id, operation_seq)
);
CREATE TABLE reservation_allocations (
  reservation_id uuid NOT NULL REFERENCES reservations(id), grant_id uuid NOT NULL REFERENCES credit_grants(id),
  amount bigint NOT NULL CHECK (amount > 0), PRIMARY KEY(reservation_id, grant_id)
);
CREATE TABLE credit_ledger (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), wallet_id uuid NOT NULL REFERENCES wallets(id),
  grant_id uuid REFERENCES credit_grants(id), reservation_id uuid REFERENCES reservations(id),
  operation_ref text NOT NULL UNIQUE, kind text NOT NULL CHECK (kind IN ('grant','reserve','settle','release','expire','refund')),
  available_delta bigint NOT NULL, reserved_delta bigint NOT NULL, created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE usage_events (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), wallet_id uuid NOT NULL REFERENCES wallets(id),
  reservation_id uuid REFERENCES reservations(id), external_id text NOT NULL UNIQUE, units bigint NOT NULL CHECK (units >= 0),
  occurred_at timestamptz NOT NULL, created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE payments (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), account_id uuid NOT NULL REFERENCES billing_accounts(id),
  provider text NOT NULL, provider_order_id text, provider_payment_id text, status payment_status NOT NULL,
  amount_minor bigint NOT NULL CHECK (amount_minor >= 0), currency char(3) NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(provider, provider_order_id), UNIQUE(provider, provider_payment_id)
);
CREATE TABLE provider_events (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), provider text NOT NULL, provider_event_id text NOT NULL,
  event_type text NOT NULL, payload jsonb NOT NULL, processed_at timestamptz, created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(provider, provider_event_id)
);
CREATE TABLE outbox_events (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), aggregate_type text NOT NULL, aggregate_id uuid NOT NULL,
  event_type text NOT NULL, sequence bigint GENERATED ALWAYS AS IDENTITY, payload jsonb NOT NULL,
  published_at timestamptz, created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX outbox_sequence_idx ON outbox_events(sequence);
CREATE TABLE webhook_deliveries (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), event_id uuid NOT NULL REFERENCES outbox_events(id),
  target_url text NOT NULL, status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','delivered','failed')),
  attempts integer NOT NULL DEFAULT 0, next_attempt_at timestamptz NOT NULL DEFAULT now(), last_error text,
  delivered_at timestamptz, UNIQUE(event_id, target_url)
);

-- +goose Down
DROP TABLE webhook_deliveries, outbox_events, provider_events, payments, usage_events, credit_ledger,
  reservation_allocations, reservations, credit_grants, wallets, subscriptions, plans, products, account_links, billing_accounts;
DROP TYPE payment_status;
DROP TYPE reservation_status;
DROP TYPE subscription_status;
