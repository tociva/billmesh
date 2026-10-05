-- +goose Up
-- Older installations may already contain the catalogue offers that were
-- formerly inserted by migrations 00003 and 00008. Preserve referenced rows
-- for billing history, but remove them from every customer-facing catalogue.
UPDATE plans
SET active = false,
    selectable = false,
    default_for_product = false,
    checkout_enabled = false,
    effective_to = COALESCE(effective_to, now()),
    version = version + 1,
    updated_at = now()
WHERE product_id = (SELECT id FROM products WHERE slug = 'daybook')
  AND slug IN (
    'daybook-free',
    'daybook-paid',
    'daybook-basic-monthly',
    'daybook-basic-annual',
    'daybook-professional-monthly',
    'daybook-professional-annual'
  )
  AND (active OR selectable OR default_for_product OR checkout_enabled OR effective_to IS NULL);

UPDATE plans
SET active = false,
    selectable = false,
    default_for_product = false,
    checkout_enabled = false,
    effective_to = COALESCE(effective_to, now()),
    version = version + 1,
    updated_at = now()
WHERE product_id = (SELECT id FROM products WHERE slug = 'taskmesh')
  AND slug = 'professional'
  AND (active OR selectable OR default_for_product OR checkout_enabled OR effective_to IS NULL);

UPDATE credit_packs
SET active = false
WHERE product_id = (SELECT id FROM products WHERE slug = 'daybook')
  AND slug = 'credits-500'
  AND active;

-- +goose Down
UPDATE plans
SET active = true,
    selectable = true,
    default_for_product = false,
    checkout_enabled = true,
    effective_to = NULL,
    version = version + 1,
    updated_at = now()
WHERE product_id = (SELECT id FROM products WHERE slug = 'daybook')
  AND slug IN (
    'daybook-free',
    'daybook-paid',
    'daybook-basic-monthly',
    'daybook-basic-annual',
    'daybook-professional-monthly',
    'daybook-professional-annual'
  );

UPDATE plans
SET default_for_product = true,
    version = version + 1,
    updated_at = now()
WHERE product_id = (SELECT id FROM products WHERE slug = 'daybook')
  AND slug = 'daybook-free'
  AND NOT EXISTS (
    SELECT 1
    FROM plans existing
    WHERE existing.product_id = plans.product_id
      AND existing.active
      AND existing.default_for_product
  );

UPDATE plans
SET active = true,
    selectable = true,
    default_for_product = false,
    checkout_enabled = true,
    effective_to = NULL,
    version = version + 1,
    updated_at = now()
WHERE product_id = (SELECT id FROM products WHERE slug = 'taskmesh')
  AND slug = 'professional';

UPDATE credit_packs
SET active = true
WHERE product_id = (SELECT id FROM products WHERE slug = 'daybook')
  AND slug = 'credits-500';
