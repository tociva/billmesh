-- +goose Up
ALTER TABLE products ALTER COLUMN billing_policy SET DEFAULT '{
  "schema_version":1,
  "customer":{"scope":"identity","free_allowance":1,"ownership_change":"retain","ownership_transfer":"unsupported","ineligible_owner_action":"require_paid_checkout"},
  "onboarding":{"allow_without_subscription":true,"initial_plan":"explicit_transition","ineligible_action":"require_paid_checkout","deletion_retention":"retain"},
  "catalogue":{"access":"application_token","required_before_account":false,"presentation_fields":["description","price","entitlements","availability"],"trial_enabled":false,"trial_days":0,"trial_conversion":"expire"},
  "lifecycle":{"free_to_paid":"immediate_after_capture","paid_to_paid":"checkout","downgrade":"period_end","cancellation_default":"period_end","allow_immediate_cancel":true,"immediate_cancel_refund":"none","allow_cancellation_withdraw":false,"reactivation":"new_transition","over_limit":"block_new","renewal":"manual","grace_period_days":3,"dunning":"grace_period","expiration":"cancel","refund_entitlements":"revoke","chargeback_entitlements":"revoke"},
  "projection":{"fresh_seconds":300,"degraded_seconds":0,"fail_closed_operations":["subscription_change","credit_purchase","limit_increase"],"refresh_seconds":60,"reconciliation_seconds":300},
  "checkout":{"allowed_redirect_origins":[],"presentation":"modal","recurring_mandate":false,"confirmation":"webhook"}
}'::jsonb;

UPDATE products
SET billing_policy = jsonb_set(
      jsonb_set(billing_policy, '{lifecycle,renewal}', '"manual"'::jsonb),
      '{checkout,recurring_mandate}', 'false'::jsonb
    ),
    billing_policy_version = billing_policy_version + 1,
    updated_at = now()
WHERE billing_policy #>> '{lifecycle,renewal}' IS DISTINCT FROM 'manual'
   OR billing_policy #>> '{checkout,recurring_mandate}' IS DISTINCT FROM 'false';

UPDATE subscription_transitions
SET billing_policy = jsonb_set(
      jsonb_set(billing_policy, '{lifecycle,renewal}', '"manual"'::jsonb),
      '{checkout,recurring_mandate}', 'false'::jsonb
    )
WHERE billing_policy <> '{}'::jsonb
  AND (billing_policy #>> '{lifecycle,renewal}' IS DISTINCT FROM 'manual'
    OR billing_policy #>> '{checkout,recurring_mandate}' IS DISTINCT FROM 'false');

UPDATE subscriptions
SET billing_policy = jsonb_set(
      jsonb_set(billing_policy, '{lifecycle,renewal}', '"manual"'::jsonb),
      '{checkout,recurring_mandate}', 'false'::jsonb
    ),
    updated_at = now()
WHERE billing_policy <> '{}'::jsonb
  AND (billing_policy #>> '{lifecycle,renewal}' IS DISTINCT FROM 'manual'
    OR billing_policy #>> '{checkout,recurring_mandate}' IS DISTINCT FROM 'false');

-- +goose Down
ALTER TABLE products ALTER COLUMN billing_policy SET DEFAULT '{
  "schema_version":1,
  "customer":{"scope":"identity","free_allowance":1,"ownership_change":"retain","ownership_transfer":"unsupported","ineligible_owner_action":"require_paid_checkout"},
  "onboarding":{"allow_without_subscription":true,"initial_plan":"explicit_transition","ineligible_action":"require_paid_checkout","deletion_retention":"retain"},
  "catalogue":{"access":"application_token","required_before_account":false,"presentation_fields":["description","price","entitlements","availability"],"trial_enabled":false,"trial_days":0,"trial_conversion":"expire"},
  "lifecycle":{"free_to_paid":"immediate_after_capture","paid_to_paid":"checkout","downgrade":"period_end","cancellation_default":"period_end","allow_immediate_cancel":true,"immediate_cancel_refund":"none","allow_cancellation_withdraw":false,"reactivation":"new_transition","over_limit":"block_new","renewal":"provider_event","grace_period_days":3,"dunning":"grace_period","expiration":"cancel","refund_entitlements":"revoke","chargeback_entitlements":"revoke"},
  "projection":{"fresh_seconds":300,"degraded_seconds":0,"fail_closed_operations":["subscription_change","credit_purchase","limit_increase"],"refresh_seconds":60,"reconciliation_seconds":300},
  "checkout":{"allowed_redirect_origins":[],"presentation":"provider_hosted","recurring_mandate":false,"confirmation":"webhook"}
}'::jsonb;

UPDATE products
SET billing_policy = jsonb_set(billing_policy, '{lifecycle,renewal}', '"provider_event"'::jsonb),
    billing_policy_version = billing_policy_version + 1,
    updated_at = now();

UPDATE subscription_transitions
SET billing_policy = jsonb_set(billing_policy, '{lifecycle,renewal}', '"provider_event"'::jsonb)
WHERE billing_policy <> '{}'::jsonb;

UPDATE subscriptions
SET billing_policy = jsonb_set(billing_policy, '{lifecycle,renewal}', '"provider_event"'::jsonb),
    updated_at = now()
WHERE billing_policy <> '{}'::jsonb;
