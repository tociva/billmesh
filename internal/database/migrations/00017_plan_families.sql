-- +goose Up
CREATE EXTENSION IF NOT EXISTS btree_gist;

ALTER TABLE plans
  ADD COLUMN plan_family_id text;

UPDATE plans p
SET plan_family_id = CASE
  WHEN pr.slug = 'daybook' AND p.slug IN ('daybook-basic-monthly', 'daybook-basic-annual') THEN 'basic'
  WHEN pr.slug = 'daybook' AND p.slug IN ('daybook-professional-monthly', 'daybook-professional-annual') THEN 'professional'
  WHEN pr.slug = 'daybook' AND p.slug = 'daybook-free' THEN 'free'
  WHEN pr.slug = 'daybook' AND p.slug = 'daybook-paid' THEN 'paid'
  WHEN pr.slug = 'taskmesh' AND p.slug = 'professional' THEN 'professional'
  ELSE p.slug
END
FROM products pr
WHERE pr.id = p.product_id;

ALTER TABLE plans
  ALTER COLUMN plan_family_id SET NOT NULL,
  ADD CONSTRAINT plans_plan_family_id_format_check CHECK (
    char_length(plan_family_id) BETWEEN 2 AND 63
    AND plan_family_id ~ '^[a-z][a-z0-9]*(-[a-z0-9]+)*$'
  ),
  ADD CONSTRAINT plans_family_interval_effective_excl EXCLUDE USING gist (
    product_id WITH =,
    plan_family_id WITH =,
    billing_interval WITH =,
    tstzrange(effective_from, effective_to, '[)') WITH &&
  ) WHERE (active AND selectable);

ALTER TABLE subscription_transitions
  ADD COLUMN target_plan_family_id text;

UPDATE subscription_transitions t
SET target_plan_family_id = p.plan_family_id
FROM plans p
WHERE p.id = t.target_plan_id;

ALTER TABLE subscription_transitions
  ALTER COLUMN target_plan_family_id SET NOT NULL;

ALTER TABLE subscriptions
  ADD COLUMN plan_family_id text;

UPDATE subscriptions s
SET plan_family_id = p.plan_family_id
FROM plans p
WHERE p.id = s.plan_id;

ALTER TABLE subscriptions
  ALTER COLUMN plan_family_id SET NOT NULL;

-- +goose Down
ALTER TABLE subscriptions
  DROP COLUMN plan_family_id;

ALTER TABLE subscription_transitions
  DROP COLUMN target_plan_family_id;

ALTER TABLE plans
  DROP CONSTRAINT plans_family_interval_effective_excl,
  DROP CONSTRAINT plans_plan_family_id_format_check,
  DROP COLUMN plan_family_id;
