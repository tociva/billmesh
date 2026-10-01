-- +goose Up
ALTER TABLE products
  ADD COLUMN description text NOT NULL DEFAULT '',
  ADD COLUMN active boolean NOT NULL DEFAULT true,
  ADD COLUMN version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
  ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now();

ALTER TABLE plans
  ADD COLUMN version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
  ADD COLUMN created_at timestamptz NOT NULL DEFAULT now(),
  ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now();

ALTER TABLE subscriptions
  ADD COLUMN included_credits bigint NOT NULL DEFAULT 0 CHECK (included_credits >= 0),
  ADD COLUMN entitlements jsonb NOT NULL DEFAULT '{}';

UPDATE subscriptions s
SET included_credits = p.included_credits,
    entitlements = p.entitlements
FROM plans p
WHERE p.id = s.plan_id;

CREATE INDEX products_active_slug_idx ON products(active, slug);
CREATE INDEX plans_product_active_price_idx ON plans(product_id, active, price_minor, slug);

-- +goose Down
DROP INDEX plans_product_active_price_idx;
DROP INDEX products_active_slug_idx;

ALTER TABLE subscriptions
  DROP COLUMN entitlements,
  DROP COLUMN included_credits;

ALTER TABLE plans
  DROP COLUMN updated_at,
  DROP COLUMN created_at,
  DROP COLUMN version;

ALTER TABLE products
  DROP COLUMN updated_at,
  DROP COLUMN version,
  DROP COLUMN active,
  DROP COLUMN description;
