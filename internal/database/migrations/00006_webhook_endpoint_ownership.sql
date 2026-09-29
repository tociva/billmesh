-- +goose Up
ALTER TABLE webhook_deliveries ADD COLUMN endpoint_id uuid REFERENCES webhook_endpoints(id);

-- Legacy deliveries cannot be assigned safely by URL because multiple tenants may share it.
-- They remain pending for an explicit administrative decision instead of being sent unsigned.

-- +goose Down
ALTER TABLE webhook_deliveries DROP COLUMN endpoint_id;
