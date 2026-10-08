-- +goose Up
ALTER TABLE billing_customers
  ADD COLUMN authorizer_client_id text NOT NULL DEFAULT '';

ALTER TABLE billing_customers
  DROP CONSTRAINT billing_customers_issuer_external_subject_key;

ALTER TABLE billing_customers
  ADD CONSTRAINT billing_customers_authority_subject_key
  UNIQUE (issuer, authorizer_client_id, external_subject);

-- +goose Down
ALTER TABLE billing_customers
  DROP CONSTRAINT billing_customers_authority_subject_key;

ALTER TABLE billing_customers
  DROP COLUMN authorizer_client_id;

ALTER TABLE billing_customers
  ADD CONSTRAINT billing_customers_issuer_external_subject_key
  UNIQUE (issuer, external_subject);
