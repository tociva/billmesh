-- +goose Up
ALTER TABLE payments
  ADD COLUMN request_id text,
  ADD CONSTRAINT payments_request_id_format
    CHECK (request_id IS NULL OR request_id ~ '^[A-Za-z0-9._:-]{1,128}$');

CREATE INDEX payments_request_id_idx ON payments(request_id) WHERE request_id IS NOT NULL;

ALTER TABLE provider_events
  ADD COLUMN billmesh_payment_id uuid REFERENCES payments(id),
  ADD COLUMN request_id text,
  ADD COLUMN provider_order_id text,
  ADD COLUMN provider_payment_id text,
  ADD COLUMN provider_refund_id text,
  ADD CONSTRAINT provider_events_request_id_format
    CHECK (request_id IS NULL OR request_id ~ '^[A-Za-z0-9._:-]{1,128}$');

CREATE INDEX provider_events_billmesh_payment_idx ON provider_events(billmesh_payment_id) WHERE billmesh_payment_id IS NOT NULL;
CREATE INDEX provider_events_request_id_idx ON provider_events(request_id) WHERE request_id IS NOT NULL;
CREATE INDEX provider_events_order_ref_idx ON provider_events(provider,provider_order_id) WHERE provider_order_id IS NOT NULL;
CREATE INDEX provider_events_payment_ref_idx ON provider_events(provider,provider_payment_id) WHERE provider_payment_id IS NOT NULL;
CREATE INDEX provider_events_refund_ref_idx ON provider_events(provider,provider_refund_id) WHERE provider_refund_id IS NOT NULL;

-- +goose Down
DROP INDEX provider_events_refund_ref_idx;
DROP INDEX provider_events_payment_ref_idx;
DROP INDEX provider_events_order_ref_idx;
DROP INDEX provider_events_request_id_idx;
DROP INDEX provider_events_billmesh_payment_idx;
ALTER TABLE provider_events
  DROP CONSTRAINT provider_events_request_id_format,
  DROP COLUMN provider_refund_id,
  DROP COLUMN provider_payment_id,
  DROP COLUMN provider_order_id,
  DROP COLUMN request_id,
  DROP COLUMN billmesh_payment_id;
DROP INDEX payments_request_id_idx;
ALTER TABLE payments DROP CONSTRAINT payments_request_id_format, DROP COLUMN request_id;
