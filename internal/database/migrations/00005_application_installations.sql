-- +goose Up
CREATE TABLE application_installations (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id uuid NOT NULL REFERENCES billing_accounts(id) ON DELETE CASCADE,
  application text NOT NULL,
  organization_id text NOT NULL,
  active boolean NOT NULL DEFAULT true,
  active_execution_id text,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(account_id, application, organization_id)
);

-- +goose Down
DROP TABLE application_installations;
