-- +goose Up
ALTER TABLE products
  ADD COLUMN entitlement_schema jsonb NOT NULL DEFAULT '{"fields":[]}'::jsonb,
  ADD COLUMN entitlement_schema_version bigint NOT NULL DEFAULT 1 CHECK (entitlement_schema_version > 0),
  ADD CONSTRAINT products_entitlement_schema_object CHECK (jsonb_typeof(entitlement_schema) = 'object');

UPDATE products
SET entitlement_schema = '{"fields":[
  {"key":"branches","label":"Maximum branches","type":"integer","required":true,"default":1,"minimum":0},
  {"key":"users","label":"Maximum users","type":"integer","required":true,"default":1,"minimum":0},
  {"key":"serviceusers","label":"Maximum service users","type":"integer","required":true,"default":0,"minimum":0},
  {"key":"workflow_execution","label":"Workflow execution","type":"boolean","required":true,"default":true},
  {"key":"standalone_workflow","label":"Standalone workflow","type":"boolean","required":true,"default":false}
]}'::jsonb
WHERE slug = 'daybook';

UPDATE products
SET entitlement_schema = '{"fields":[
  {"key":"workflow_execution","label":"Workflow execution","type":"boolean","required":true,"default":true},
  {"key":"standalone_workflow","label":"Standalone workflow","type":"boolean","required":true,"default":true}
]}'::jsonb
WHERE slug = 'taskmesh';

UPDATE plans
SET entitlements = '{"branches":1,"users":1,"serviceusers":0,"workflow_execution":true,"standalone_workflow":false}'::jsonb || entitlements
WHERE product_id = (SELECT id FROM products WHERE slug = 'daybook');

-- +goose Down
ALTER TABLE products
  DROP CONSTRAINT products_entitlement_schema_object,
  DROP COLUMN entitlement_schema_version,
  DROP COLUMN entitlement_schema;
