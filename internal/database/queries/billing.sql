-- name: GetWallet :one
SELECT id, account_id, product_id, available, reserved, created_at FROM wallets WHERE id = $1;

-- name: ListSpendableGrantsForUpdate :many
SELECT id, wallet_id, source, operation_ref, amount, remaining, expires_at, created_at
FROM credit_grants WHERE wallet_id = $1 AND remaining > 0 AND (expires_at IS NULL OR expires_at > now())
ORDER BY expires_at ASC NULLS LAST, created_at ASC FOR UPDATE;

-- name: ListEventsAfter :many
SELECT id, aggregate_type, aggregate_id, event_type, sequence, payload, created_at
FROM outbox_events WHERE sequence > $1 ORDER BY sequence LIMIT $2;
