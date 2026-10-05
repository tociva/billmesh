-- Test stack only. Production catalogue records are configured through the
-- catalogue administration API rather than schema migrations.
INSERT INTO plans(
  product_id, slug, name, price_minor, currency, included_credits,
  entitlements, active, billing_interval, billing_model, selectable,
  default_for_product, checkout_enabled
)
SELECT id, 'daybook-free', 'Daybook Free', 0, 'INR', 100,
       '{"branches":1,"users":1,"serviceusers":0,"workflow_execution":true,"standalone_workflow":false}'::jsonb,
       true, 'monthly', 'free', true, true, true
FROM products WHERE slug = 'daybook'
ON CONFLICT(product_id, slug) DO NOTHING;

INSERT INTO plans(
  product_id, slug, name, price_minor, currency, included_credits,
  entitlements, active, billing_interval, billing_model, selectable,
  default_for_product, checkout_enabled
)
SELECT id, fixture.slug, fixture.name, fixture.price_minor, 'INR', fixture.included_credits,
       fixture.entitlements, true, fixture.billing_interval, 'paid', true, false, true
FROM products
CROSS JOIN (VALUES
  ('daybook-paid', 'Daybook Paid', 99900::bigint, 1000::bigint, 'monthly',
   '{"branches":1,"users":1,"serviceusers":0,"workflow_execution":true,"standalone_workflow":false}'::jsonb),
  ('daybook-basic-monthly', 'Daybook Basic', 10000::bigint, 1000::bigint, 'monthly',
   '{"branches":1,"users":1,"serviceusers":0,"workflow_execution":true,"standalone_workflow":false}'::jsonb),
  ('daybook-basic-annual', 'Daybook Basic', 100000::bigint, 1000::bigint, 'annual',
   '{"branches":1,"users":1,"serviceusers":0,"workflow_execution":true,"standalone_workflow":false}'::jsonb),
  ('daybook-professional-monthly', 'Daybook Professional', 50000::bigint, 1000::bigint, 'monthly',
   '{"branches":3,"users":6,"serviceusers":1,"workflow_execution":true,"standalone_workflow":false}'::jsonb),
  ('daybook-professional-annual', 'Daybook Professional', 500000::bigint, 1000::bigint, 'annual',
   '{"branches":3,"users":6,"serviceusers":1,"workflow_execution":true,"standalone_workflow":false}'::jsonb)
) AS fixture(slug, name, price_minor, included_credits, billing_interval, entitlements)
WHERE products.slug = 'daybook'
ON CONFLICT(product_id, slug) DO NOTHING;

INSERT INTO plans(
  product_id, slug, name, price_minor, currency, included_credits,
  entitlements, active, billing_interval, billing_model, selectable,
  default_for_product, checkout_enabled
)
SELECT id, 'professional', 'Taskmesh Professional', 149900, 'INR', 1000,
       '{"workflow_execution":true,"standalone_workflow":true}'::jsonb,
       true, 'monthly', 'paid', true, false, true
FROM products WHERE slug = 'taskmesh'
ON CONFLICT(product_id, slug) DO NOTHING;

INSERT INTO credit_packs(
  product_id, slug, name, credits, price_minor, currency, validity_days
)
SELECT id, 'credits-500', '500 Credits', 500, 49900, 'INR', NULL
FROM products WHERE slug = 'daybook'
ON CONFLICT(product_id, slug) DO NOTHING;
