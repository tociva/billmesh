-- +goose Up
ALTER TABLE products
  ADD COLUMN billing_policy jsonb NOT NULL DEFAULT '{
    "schema_version":1,
    "customer":{"scope":"identity","free_allowance":1,"ownership_change":"retain","ownership_transfer":"unsupported","ineligible_owner_action":"require_paid_checkout"},
    "onboarding":{"allow_without_subscription":true,"initial_plan":"explicit_transition","ineligible_action":"require_paid_checkout","deletion_retention":"retain"},
    "catalogue":{"access":"application_token","required_before_account":false,"presentation_fields":["description","price","entitlements","availability"],"trial_enabled":false,"trial_days":0,"trial_conversion":"expire"},
    "lifecycle":{"free_to_paid":"immediate_after_capture","paid_to_paid":"checkout","downgrade":"period_end","cancellation_default":"period_end","allow_immediate_cancel":true,"immediate_cancel_refund":"none","allow_cancellation_withdraw":false,"reactivation":"new_transition","over_limit":"block_new","renewal":"provider_event","grace_period_days":3,"dunning":"grace_period","expiration":"cancel","refund_entitlements":"revoke","chargeback_entitlements":"revoke"},
    "projection":{"fresh_seconds":300,"degraded_seconds":0,"fail_closed_operations":["subscription_change","credit_purchase","limit_increase"],"refresh_seconds":60,"reconciliation_seconds":300},
    "checkout":{"allowed_redirect_origins":[],"presentation":"provider_hosted","recurring_mandate":false,"confirmation":"webhook"}
  }'::jsonb,
  ADD COLUMN billing_policy_version bigint NOT NULL DEFAULT 1 CHECK (billing_policy_version > 0),
  ADD CONSTRAINT products_billing_policy_object CHECK (jsonb_typeof(billing_policy) = 'object');

ALTER TABLE subscription_transitions
  ADD COLUMN billing_policy jsonb NOT NULL DEFAULT '{}'::jsonb,
  ADD COLUMN billing_policy_version bigint NOT NULL DEFAULT 1 CHECK (billing_policy_version > 0),
  ADD COLUMN success_url text,
  ADD COLUMN cancel_url text,
  ADD CONSTRAINT transitions_billing_policy_object CHECK (jsonb_typeof(billing_policy) = 'object');

UPDATE subscription_transitions t
SET billing_policy = p.billing_policy,
    billing_policy_version = p.billing_policy_version
FROM products p
WHERE p.id = t.product_id;

ALTER TABLE subscriptions
  ADD COLUMN billing_policy jsonb NOT NULL DEFAULT '{}'::jsonb,
  ADD COLUMN billing_policy_version bigint NOT NULL DEFAULT 1 CHECK (billing_policy_version > 0),
  ADD CONSTRAINT subscriptions_billing_policy_object CHECK (jsonb_typeof(billing_policy) = 'object');

UPDATE subscriptions s
SET billing_policy = p.billing_policy,
    billing_policy_version = p.billing_policy_version
FROM products p
WHERE p.id = s.product_id;

-- +goose Down
ALTER TABLE subscriptions
  DROP CONSTRAINT subscriptions_billing_policy_object,
  DROP COLUMN billing_policy_version,
  DROP COLUMN billing_policy;

ALTER TABLE subscription_transitions
  DROP CONSTRAINT transitions_billing_policy_object,
  DROP COLUMN cancel_url,
  DROP COLUMN success_url,
  DROP COLUMN billing_policy_version,
  DROP COLUMN billing_policy;

ALTER TABLE products
  DROP CONSTRAINT products_billing_policy_object,
  DROP COLUMN billing_policy_version,
  DROP COLUMN billing_policy;
