-- Test stack only. Production catalogue records are configured through the
-- catalogue administration API rather than schema migrations.
INSERT INTO products(slug, name, description, entitlement_schema)
VALUES (
  'daybook',
  'Daybook',
  'Daybook test catalogue',
  '{"fields":[
    {"key":"branches","label":"Maximum branches","type":"integer","required":true,"default":1,"minimum":0},
    {"key":"users","label":"Maximum users","type":"integer","required":true,"default":1,"minimum":0},
    {"key":"serviceusers","label":"Maximum service users","type":"integer","required":true,"default":0,"minimum":0},
    {"key":"workflow_execution","label":"Workflow execution","type":"boolean","required":true,"default":true},
    {"key":"standalone_workflow","label":"Standalone workflow","type":"boolean","required":true,"default":false}
  ]}'::jsonb
)
ON CONFLICT(slug) DO NOTHING;

INSERT INTO products(slug, name, description, entitlement_schema)
VALUES (
  'taskmesh',
  'Taskmesh',
  'Taskmesh test catalogue',
  '{"fields":[
    {"key":"workflow_execution","label":"Workflow execution","type":"boolean","required":true,"default":true},
    {"key":"standalone_workflow","label":"Standalone workflow","type":"boolean","required":true,"default":true}
  ]}'::jsonb
)
ON CONFLICT(slug) DO NOTHING;

UPDATE products
SET billing_policy = jsonb_set(billing_policy, '{checkout,presentation}', '"modal"'::jsonb, false)
WHERE slug IN ('daybook', 'taskmesh')
  AND billing_policy->'checkout'->>'presentation' = 'provider_hosted';

INSERT INTO plans(
  product_id, slug, plan_family_id, name, price_minor, currency, included_credits,
  entitlements, active, billing_interval, billing_model, selectable,
  default_for_product, checkout_enabled
)
SELECT id, 'daybook-free', 'free', 'Daybook Free', 0, 'INR', 100,
       '{"branches":1,"users":1,"serviceusers":0,"workflow_execution":true,"standalone_workflow":false}'::jsonb,
       true, 'monthly', 'free', true, true, true
FROM products WHERE slug = 'daybook'
ON CONFLICT(product_id, slug) DO NOTHING;

INSERT INTO plans(
  product_id, slug, plan_family_id, name, price_minor, currency, included_credits,
  entitlements, active, billing_interval, billing_model, selectable,
  default_for_product, checkout_enabled
)
SELECT id, fixture.slug, fixture.plan_family_id, fixture.name, fixture.price_minor, 'INR', fixture.included_credits,
       fixture.entitlements, true, fixture.billing_interval, 'paid', true, false, true
FROM products
CROSS JOIN (VALUES
  ('daybook-paid', 'paid', 'Daybook Paid', 99900::bigint, 1000::bigint, 'monthly',
   '{"branches":1,"users":1,"serviceusers":0,"workflow_execution":true,"standalone_workflow":false}'::jsonb),
  ('daybook-basic-monthly', 'basic', 'Daybook Basic', 10000::bigint, 1000::bigint, 'monthly',
   '{"branches":1,"users":1,"serviceusers":0,"workflow_execution":true,"standalone_workflow":false}'::jsonb),
  ('daybook-basic-annual', 'basic', 'Daybook Basic', 100000::bigint, 1000::bigint, 'annual',
   '{"branches":1,"users":1,"serviceusers":0,"workflow_execution":true,"standalone_workflow":false}'::jsonb),
  ('daybook-professional-monthly', 'professional', 'Daybook Professional', 50000::bigint, 1000::bigint, 'monthly',
   '{"branches":3,"users":6,"serviceusers":1,"workflow_execution":true,"standalone_workflow":false}'::jsonb),
  ('daybook-professional-annual', 'professional', 'Daybook Professional', 500000::bigint, 1000::bigint, 'annual',
   '{"branches":3,"users":6,"serviceusers":1,"workflow_execution":true,"standalone_workflow":false}'::jsonb)
) AS fixture(slug, plan_family_id, name, price_minor, included_credits, billing_interval, entitlements)
WHERE products.slug = 'daybook'
ON CONFLICT(product_id, slug) DO NOTHING;

INSERT INTO plans(
  product_id, slug, plan_family_id, name, price_minor, currency, included_credits,
  entitlements, active, billing_interval, billing_model, selectable,
  default_for_product, checkout_enabled
)
SELECT id, 'professional', 'professional', 'Taskmesh Professional', 149900, 'INR', 1000,
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
