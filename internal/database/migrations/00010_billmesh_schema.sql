-- +goose Up
CREATE SCHEMA IF NOT EXISTS billmesh;

ALTER TYPE subscription_status SET SCHEMA billmesh;
ALTER TYPE reservation_status SET SCHEMA billmesh;
ALTER TYPE payment_status SET SCHEMA billmesh;

ALTER FUNCTION prevent_finalized_invoice_mutation() SET SCHEMA billmesh;
ALTER FUNCTION prevent_audit_log_mutation() SET SCHEMA billmesh;
ALTER FUNCTION prevent_credit_ledger_mutation() SET SCHEMA billmesh;

ALTER TABLE billing_accounts SET SCHEMA billmesh;
ALTER TABLE account_links SET SCHEMA billmesh;
ALTER TABLE products SET SCHEMA billmesh;
ALTER TABLE plans SET SCHEMA billmesh;
ALTER TABLE subscriptions SET SCHEMA billmesh;
ALTER TABLE wallets SET SCHEMA billmesh;
ALTER TABLE credit_grants SET SCHEMA billmesh;
ALTER TABLE reservations SET SCHEMA billmesh;
ALTER TABLE reservation_allocations SET SCHEMA billmesh;
ALTER TABLE credit_ledger SET SCHEMA billmesh;
ALTER TABLE usage_events SET SCHEMA billmesh;
ALTER TABLE payments SET SCHEMA billmesh;
ALTER TABLE provider_events SET SCHEMA billmesh;
ALTER TABLE outbox_events SET SCHEMA billmesh;
ALTER TABLE webhook_deliveries SET SCHEMA billmesh;
ALTER TABLE invoices SET SCHEMA billmesh;
ALTER TABLE invoice_lines SET SCHEMA billmesh;
ALTER TABLE audit_log SET SCHEMA billmesh;
ALTER TABLE subscription_history SET SCHEMA billmesh;
ALTER TABLE credit_packs SET SCHEMA billmesh;
ALTER TABLE refunds SET SCHEMA billmesh;
ALTER TABLE credit_notes SET SCHEMA billmesh;
ALTER TABLE webhook_endpoints SET SCHEMA billmesh;
ALTER TABLE threshold_notifications SET SCHEMA billmesh;
ALTER TABLE application_installations SET SCHEMA billmesh;
ALTER TABLE browser_login_transactions SET SCHEMA billmesh;
ALTER TABLE browser_sessions SET SCHEMA billmesh;
ALTER TABLE browser_logout_transactions SET SCHEMA billmesh;

-- +goose Down
ALTER TABLE billmesh.billing_accounts SET SCHEMA public;
ALTER TABLE billmesh.account_links SET SCHEMA public;
ALTER TABLE billmesh.products SET SCHEMA public;
ALTER TABLE billmesh.plans SET SCHEMA public;
ALTER TABLE billmesh.subscriptions SET SCHEMA public;
ALTER TABLE billmesh.wallets SET SCHEMA public;
ALTER TABLE billmesh.credit_grants SET SCHEMA public;
ALTER TABLE billmesh.reservations SET SCHEMA public;
ALTER TABLE billmesh.reservation_allocations SET SCHEMA public;
ALTER TABLE billmesh.credit_ledger SET SCHEMA public;
ALTER TABLE billmesh.usage_events SET SCHEMA public;
ALTER TABLE billmesh.payments SET SCHEMA public;
ALTER TABLE billmesh.provider_events SET SCHEMA public;
ALTER TABLE billmesh.outbox_events SET SCHEMA public;
ALTER TABLE billmesh.webhook_deliveries SET SCHEMA public;
ALTER TABLE billmesh.invoices SET SCHEMA public;
ALTER TABLE billmesh.invoice_lines SET SCHEMA public;
ALTER TABLE billmesh.audit_log SET SCHEMA public;
ALTER TABLE billmesh.subscription_history SET SCHEMA public;
ALTER TABLE billmesh.credit_packs SET SCHEMA public;
ALTER TABLE billmesh.refunds SET SCHEMA public;
ALTER TABLE billmesh.credit_notes SET SCHEMA public;
ALTER TABLE billmesh.webhook_endpoints SET SCHEMA public;
ALTER TABLE billmesh.threshold_notifications SET SCHEMA public;
ALTER TABLE billmesh.application_installations SET SCHEMA public;
ALTER TABLE billmesh.browser_login_transactions SET SCHEMA public;
ALTER TABLE billmesh.browser_sessions SET SCHEMA public;
ALTER TABLE billmesh.browser_logout_transactions SET SCHEMA public;

ALTER FUNCTION billmesh.prevent_finalized_invoice_mutation() SET SCHEMA public;
ALTER FUNCTION billmesh.prevent_audit_log_mutation() SET SCHEMA public;
ALTER FUNCTION billmesh.prevent_credit_ledger_mutation() SET SCHEMA public;

ALTER TYPE billmesh.subscription_status SET SCHEMA public;
ALTER TYPE billmesh.reservation_status SET SCHEMA public;
ALTER TYPE billmesh.payment_status SET SCHEMA public;

DROP SCHEMA billmesh;
