-- +goose Up
CREATE TABLE browser_login_transactions (
  id_digest bytea PRIMARY KEY,
  payload bytea NOT NULL,
  expires_at timestamptz NOT NULL
);
CREATE INDEX browser_login_transactions_expiry_idx ON browser_login_transactions (expires_at);

CREATE TABLE browser_sessions (
  id_digest bytea PRIMARY KEY,
  payload bytea NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  last_seen_at timestamptz NOT NULL DEFAULT now(),
  idle_expires_at timestamptz NOT NULL,
  absolute_expires_at timestamptz NOT NULL,
  refresh_lock_owner text,
  refresh_lock_until timestamptz,
  CHECK (idle_expires_at <= absolute_expires_at)
);

CREATE INDEX browser_sessions_expiry_idx
  ON browser_sessions (absolute_expires_at, idle_expires_at);

CREATE TABLE browser_logout_transactions (
  id_digest bytea PRIMARY KEY,
  payload bytea NOT NULL,
  expires_at timestamptz NOT NULL
);
CREATE INDEX browser_logout_transactions_expiry_idx ON browser_logout_transactions (expires_at);

-- +goose Down
DROP TABLE browser_logout_transactions;
DROP TABLE browser_sessions;
DROP TABLE browser_login_transactions;
