-- +goose Up
CREATE TABLE delegation_client_profiles (
  authorizer_client_id text NOT NULL,
  actor_client_id text NOT NULL,
  scope text NOT NULL,
  client_type text NOT NULL,
  actor_type text NOT NULL,
  application text NOT NULL,
  environment text NOT NULL,
  context_profile_version integer NOT NULL DEFAULT 1,
  enabled boolean NOT NULL DEFAULT true,
  revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (authorizer_client_id, actor_client_id),
  CHECK (authorizer_client_id = btrim(authorizer_client_id) AND authorizer_client_id <> '' AND length(authorizer_client_id) <= 255),
  CHECK (actor_client_id = btrim(actor_client_id) AND actor_client_id <> '' AND length(actor_client_id) <= 255),
  CHECK (application = btrim(application) AND application <> '' AND length(application) <= 255),
  CHECK (environment = btrim(environment) AND environment <> '' AND length(environment) <= 255),
  CHECK (context_profile_version = 1),
  CHECK (
    (client_type = 'catalogue' AND scope = 'billmesh.catalogue' AND actor_type = 'service') OR
    (client_type = 'billing' AND scope = 'billmesh.billing' AND actor_type IN ('user', 'service')) OR
    (client_type = 'runtime' AND scope = 'billmesh.runtime' AND actor_type = 'service') OR
    (client_type = 'admin' AND scope = 'billmesh.admin' AND actor_type IN ('user', 'service'))
  ),
  CHECK (
    (application = '*' AND environment = '*' AND client_type = 'admin') OR
    (application <> '*' AND environment <> '*')
  )
);

CREATE INDEX delegation_client_profiles_enabled_idx
  ON delegation_client_profiles(authorizer_client_id, actor_client_id)
  WHERE enabled;

CREATE TABLE delegation_client_profile_events (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  authorizer_client_id text NOT NULL,
  actor_client_id text NOT NULL,
  action text NOT NULL CHECK (action IN ('insert', 'update', 'delete')),
  before_profile jsonb,
  after_profile jsonb,
  changed_by text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);

-- +goose StatementBegin
CREATE FUNCTION maintain_delegation_client_profile_revision() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF NEW IS DISTINCT FROM OLD THEN
    NEW.revision = OLD.revision + 1;
    NEW.updated_at = now();
  END IF;
  RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER delegation_client_profile_revision
BEFORE UPDATE ON delegation_client_profiles
FOR EACH ROW EXECUTE FUNCTION maintain_delegation_client_profile_revision();

-- +goose StatementBegin
CREATE FUNCTION audit_delegation_client_profile() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO delegation_client_profile_events(
    authorizer_client_id, actor_client_id, action, before_profile, after_profile, changed_by
  ) VALUES (
    CASE WHEN TG_OP = 'DELETE' THEN OLD.authorizer_client_id ELSE NEW.authorizer_client_id END,
    CASE WHEN TG_OP = 'DELETE' THEN OLD.actor_client_id ELSE NEW.actor_client_id END,
    lower(TG_OP),
    CASE WHEN TG_OP IN ('UPDATE', 'DELETE') THEN to_jsonb(OLD) END,
    CASE WHEN TG_OP IN ('INSERT', 'UPDATE') THEN to_jsonb(NEW) END,
    session_user
  );
  IF TG_OP = 'DELETE' THEN
    RETURN OLD;
  END IF;
  RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER delegation_client_profile_audit
AFTER INSERT OR UPDATE OR DELETE ON delegation_client_profiles
FOR EACH ROW EXECUTE FUNCTION audit_delegation_client_profile();

-- +goose Down
DROP TRIGGER delegation_client_profile_audit ON delegation_client_profiles;
DROP FUNCTION audit_delegation_client_profile();
DROP TRIGGER delegation_client_profile_revision ON delegation_client_profiles;
DROP FUNCTION maintain_delegation_client_profile_revision();
DROP TABLE delegation_client_profile_events;
DROP TABLE delegation_client_profiles;
