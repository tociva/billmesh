-- +goose Up
UPDATE plans SET entitlements = entitlements || '{"branches":1,"users":1,"serviceusers":0}'::jsonb
WHERE product_id = (SELECT id FROM products WHERE slug = 'daybook') AND slug = 'daybook-free';

INSERT INTO plans(product_id,slug,name,price_minor,currency,included_credits,entitlements,active,billing_interval)
SELECT id, 'daybook-basic-monthly', 'Daybook Basic', 10000, 'INR', 1000,
       '{"branches":1,"users":1,"serviceusers":0,"workflow_execution":true,"standalone_workflow":false}'::jsonb, true, 'monthly'
FROM products WHERE slug = 'daybook'
ON CONFLICT(product_id,slug) DO UPDATE SET entitlements = excluded.entitlements;
INSERT INTO plans(product_id,slug,name,price_minor,currency,included_credits,entitlements,active,billing_interval)
SELECT id, 'daybook-basic-annual', 'Daybook Basic', 100000, 'INR', 1000,
       '{"branches":1,"users":1,"serviceusers":0,"workflow_execution":true,"standalone_workflow":false}'::jsonb, true, 'annual'
FROM products WHERE slug = 'daybook'
ON CONFLICT(product_id,slug) DO UPDATE SET entitlements = excluded.entitlements;
INSERT INTO plans(product_id,slug,name,price_minor,currency,included_credits,entitlements,active,billing_interval)
SELECT id, 'daybook-professional-monthly', 'Daybook Professional', 50000, 'INR', 1000,
       '{"branches":3,"users":6,"serviceusers":1,"workflow_execution":true,"standalone_workflow":false}'::jsonb, true, 'monthly'
FROM products WHERE slug = 'daybook'
ON CONFLICT(product_id,slug) DO UPDATE SET entitlements = excluded.entitlements;
INSERT INTO plans(product_id,slug,name,price_minor,currency,included_credits,entitlements,active,billing_interval)
SELECT id, 'daybook-professional-annual', 'Daybook Professional', 500000, 'INR', 1000,
       '{"branches":3,"users":6,"serviceusers":1,"workflow_execution":true,"standalone_workflow":false}'::jsonb, true, 'annual'
FROM products WHERE slug = 'daybook'
ON CONFLICT(product_id,slug) DO UPDATE SET entitlements = excluded.entitlements;

-- The original generic Paid plan remains unchanged for existing subscriptions.

-- +goose Down
DELETE FROM plans WHERE product_id = (SELECT id FROM products WHERE slug = 'daybook')
  AND slug IN ('daybook-basic-monthly','daybook-basic-annual','daybook-professional-monthly','daybook-professional-annual')
  AND NOT EXISTS (SELECT 1 FROM subscriptions WHERE subscriptions.plan_id = plans.id);
UPDATE plans SET entitlements = entitlements - 'branches' - 'users' - 'serviceusers'
WHERE product_id = (SELECT id FROM products WHERE slug = 'daybook') AND slug = 'daybook-free';
