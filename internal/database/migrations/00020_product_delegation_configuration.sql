-- +goose Up
CREATE TABLE product_delegation_configurations (
  product_id uuid NOT NULL REFERENCES products(id) ON DELETE CASCADE,
  environment text NOT NULL,
  authorizer_client_id text NOT NULL,
  enabled boolean NOT NULL DEFAULT true,
  version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (product_id, environment),
  CHECK (environment = btrim(environment) AND environment ~ '^[a-z][a-z0-9-]{0,62}$'),
  CHECK (authorizer_client_id = btrim(authorizer_client_id) AND authorizer_client_id <> '' AND length(authorizer_client_id) <= 255)
);

CREATE UNIQUE INDEX product_delegation_configurations_authorizer_environment_idx
  ON product_delegation_configurations(authorizer_client_id, environment);

CREATE TABLE product_delegation_actors (
  product_id uuid NOT NULL,
  environment text NOT NULL,
  actor_client_id text NOT NULL,
  profile text NOT NULL,
  actor_type text NOT NULL,
  enabled boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (product_id, environment, actor_client_id),
  FOREIGN KEY (product_id, environment)
    REFERENCES product_delegation_configurations(product_id, environment) ON DELETE CASCADE,
  CHECK (actor_client_id = btrim(actor_client_id) AND actor_client_id <> '' AND length(actor_client_id) <= 255),
  CHECK (
    (profile = 'catalogue' AND actor_type = 'service') OR
    (profile = 'billing' AND actor_type IN ('user', 'service')) OR
    (profile = 'runtime' AND actor_type = 'service')
  )
);

CREATE UNIQUE INDEX product_delegation_actors_client_environment_idx
  ON product_delegation_actors(actor_client_id, environment);

CREATE TABLE product_delegation_configuration_events (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  product_id uuid NOT NULL,
  environment text NOT NULL,
  action text NOT NULL CHECK (action IN ('configure', 'disable')),
  before_configuration jsonb,
  after_configuration jsonb NOT NULL,
  changed_by text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);

-- +goose StatementBegin
CREATE FUNCTION configure_product_delegation(
  p_product_id uuid,
  p_environment text,
  p_authorizer_client_id text,
  p_actors jsonb,
  p_enabled boolean,
  p_expected_version bigint
) RETURNS bigint
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = billmesh, pg_catalog
AS $$
DECLARE
  product_slug text;
  product_active boolean;
  current_version bigint;
  saved_version bigint;
  before_configuration jsonb;
  after_configuration jsonb;
  actor_count integer;
  enabled_actor_count integer;
BEGIN
  IF p_expected_version IS NULL OR p_expected_version < 0 THEN
    RAISE EXCEPTION 'expected_version must be zero or greater' USING ERRCODE = '23514';
  END IF;
  IF p_enabled IS NULL THEN
    RAISE EXCEPTION 'enabled is required' USING ERRCODE = '23514';
  END IF;
  IF p_environment IS NULL OR btrim(p_environment) !~ '^[a-z][a-z0-9-]{0,62}$' THEN
    RAISE EXCEPTION 'invalid delegation environment' USING ERRCODE = '23514';
  END IF;
  IF p_authorizer_client_id IS NULL OR btrim(p_authorizer_client_id) = '' OR length(btrim(p_authorizer_client_id)) > 255 THEN
    RAISE EXCEPTION 'invalid authorizer client ID' USING ERRCODE = '23514';
  END IF;

  p_environment := btrim(p_environment);
  p_authorizer_client_id := btrim(p_authorizer_client_id);

  IF p_actors IS NULL OR jsonb_typeof(p_actors) <> 'array' THEN
    RAISE EXCEPTION 'actors must be an array' USING ERRCODE = '23514';
  END IF;

  SELECT count(*), count(*) FILTER (WHERE COALESCE(actor.enabled, false))
  INTO actor_count, enabled_actor_count
  FROM jsonb_to_recordset(p_actors) AS actor(client_id text, profile text, actor_type text, enabled boolean);

  IF p_enabled AND enabled_actor_count = 0 THEN
    RAISE EXCEPTION 'enabled delegation configurations require at least one enabled actor' USING ERRCODE = '23514';
  END IF;

  IF EXISTS (
    SELECT 1
    FROM jsonb_to_recordset(p_actors) AS actor(client_id text, profile text, actor_type text, enabled boolean)
    WHERE actor.client_id IS NULL OR btrim(actor.client_id) = '' OR length(btrim(actor.client_id)) > 255
       OR actor.profile IS NULL OR actor.actor_type IS NULL OR actor.enabled IS NULL
       OR btrim(actor.client_id) = p_authorizer_client_id
       OR NOT (
         (actor.profile = 'catalogue' AND actor.actor_type = 'service') OR
         (actor.profile = 'billing' AND actor.actor_type IN ('user', 'service')) OR
         (actor.profile = 'runtime' AND actor.actor_type = 'service')
       )
  ) THEN
    RAISE EXCEPTION 'invalid delegation actor binding' USING ERRCODE = '23514';
  END IF;

  IF actor_count <> (SELECT count(DISTINCT btrim(actor.client_id)) FROM jsonb_to_recordset(p_actors) AS actor(client_id text)) THEN
    RAISE EXCEPTION 'delegation actor client IDs must be unique' USING ERRCODE = '23514';
  END IF;

  SELECT slug, active INTO STRICT product_slug, product_active
  FROM products
  WHERE id = p_product_id
  FOR UPDATE;

  IF p_enabled AND NOT product_active THEN
    RAISE EXCEPTION 'archived products cannot enable delegated access' USING ERRCODE = '23514';
  END IF;

  SELECT jsonb_build_object(
    'product_id', configuration.product_id,
    'environment', configuration.environment,
    'authorizer_client_id', configuration.authorizer_client_id,
    'enabled', configuration.enabled,
    'version', configuration.version,
    'actors', COALESCE((
      SELECT jsonb_agg(jsonb_build_object(
        'client_id', actor.actor_client_id,
        'profile', actor.profile,
        'actor_type', actor.actor_type,
        'enabled', actor.enabled
      ) ORDER BY actor.actor_client_id)
      FROM product_delegation_actors actor
      WHERE actor.product_id = configuration.product_id AND actor.environment = configuration.environment
    ), '[]'::jsonb)
  ), configuration.version
  INTO before_configuration, current_version
  FROM product_delegation_configurations configuration
  WHERE configuration.product_id = p_product_id AND configuration.environment = p_environment
  FOR UPDATE;

  IF current_version IS NULL THEN
    IF p_expected_version <> 0 THEN
      RAISE EXCEPTION 'delegation configuration changed; reload and retry' USING ERRCODE = '40001';
    END IF;
    INSERT INTO product_delegation_configurations(product_id, environment, authorizer_client_id, enabled)
    VALUES (p_product_id, p_environment, p_authorizer_client_id, p_enabled)
    RETURNING version INTO saved_version;
  ELSE
    IF p_expected_version <> current_version THEN
      RAISE EXCEPTION 'delegation configuration changed; reload and retry' USING ERRCODE = '40001';
    END IF;
    UPDATE product_delegation_configurations
    SET authorizer_client_id = p_authorizer_client_id,
        enabled = p_enabled,
        version = version + 1,
        updated_at = now()
    WHERE product_id = p_product_id AND environment = p_environment
    RETURNING version INTO saved_version;
  END IF;

  DELETE FROM product_delegation_actors
  WHERE product_id = p_product_id AND environment = p_environment;

  INSERT INTO product_delegation_actors(product_id, environment, actor_client_id, profile, actor_type, enabled)
  SELECT p_product_id, p_environment, btrim(actor.client_id), actor.profile, actor.actor_type, actor.enabled
  FROM jsonb_to_recordset(p_actors) AS actor(client_id text, profile text, actor_type text, enabled boolean);

  DELETE FROM delegation_client_profiles
  WHERE application = product_slug
    AND environment = p_environment
    AND client_type IN ('catalogue', 'billing', 'runtime');

  INSERT INTO delegation_client_profiles(
    authorizer_client_id, actor_client_id, scope, client_type, actor_type,
    application, environment, context_profile_version, enabled
  )
  SELECT
    p_authorizer_client_id,
    actor.actor_client_id,
    CASE actor.profile
      WHEN 'catalogue' THEN 'billmesh.catalogue'
      WHEN 'billing' THEN 'billmesh.billing'
      WHEN 'runtime' THEN 'billmesh.runtime'
    END,
    actor.profile,
    actor.actor_type,
    product_slug,
    p_environment,
    1,
    p_enabled AND actor.enabled
  FROM product_delegation_actors actor
  WHERE actor.product_id = p_product_id AND actor.environment = p_environment;

  SELECT jsonb_build_object(
    'product_id', configuration.product_id,
    'environment', configuration.environment,
    'authorizer_client_id', configuration.authorizer_client_id,
    'enabled', configuration.enabled,
    'version', configuration.version,
    'actors', COALESCE((
      SELECT jsonb_agg(jsonb_build_object(
        'client_id', actor.actor_client_id,
        'profile', actor.profile,
        'actor_type', actor.actor_type,
        'enabled', actor.enabled
      ) ORDER BY actor.actor_client_id)
      FROM product_delegation_actors actor
      WHERE actor.product_id = configuration.product_id AND actor.environment = configuration.environment
    ), '[]'::jsonb)
  ) INTO after_configuration
  FROM product_delegation_configurations configuration
  WHERE configuration.product_id = p_product_id AND configuration.environment = p_environment;

  INSERT INTO product_delegation_configuration_events(
    product_id, environment, action, before_configuration, after_configuration, changed_by
  ) VALUES (p_product_id, p_environment, 'configure', before_configuration, after_configuration, session_user);

  RETURN saved_version;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION disable_product_delegation(p_product_id uuid) RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = billmesh, pg_catalog
AS $$
DECLARE
  product_slug text;
  configuration record;
  before_configuration jsonb;
  after_configuration jsonb;
BEGIN
  SELECT slug INTO STRICT product_slug FROM products WHERE id = p_product_id FOR UPDATE;

  FOR configuration IN
    SELECT product_id, environment, authorizer_client_id, enabled, version
    FROM product_delegation_configurations
    WHERE product_id = p_product_id AND enabled
    FOR UPDATE
  LOOP
    SELECT jsonb_build_object(
      'product_id', configuration.product_id,
      'environment', configuration.environment,
      'authorizer_client_id', configuration.authorizer_client_id,
      'enabled', configuration.enabled,
      'version', configuration.version,
      'actors', COALESCE((
        SELECT jsonb_agg(jsonb_build_object(
          'client_id', actor.actor_client_id,
          'profile', actor.profile,
          'actor_type', actor.actor_type,
          'enabled', actor.enabled
        ) ORDER BY actor.actor_client_id)
        FROM product_delegation_actors actor
        WHERE actor.product_id = configuration.product_id AND actor.environment = configuration.environment
      ), '[]'::jsonb)
    ) INTO before_configuration;

    UPDATE product_delegation_configurations
    SET enabled = false, version = version + 1, updated_at = now()
    WHERE product_id = configuration.product_id AND environment = configuration.environment
    RETURNING jsonb_build_object(
      'product_id', product_id,
      'environment', environment,
      'authorizer_client_id', authorizer_client_id,
      'enabled', enabled,
      'version', version,
      'actors', before_configuration->'actors'
    ) INTO after_configuration;

    INSERT INTO product_delegation_configuration_events(
      product_id, environment, action, before_configuration, after_configuration, changed_by
    ) VALUES (
      configuration.product_id, configuration.environment, 'disable', before_configuration, after_configuration, session_user
    );
  END LOOP;

  UPDATE delegation_client_profiles
  SET enabled = false
  WHERE application = product_slug AND client_type IN ('catalogue', 'billing', 'runtime') AND enabled;
END;
$$;
-- +goose StatementEnd

REVOKE ALL ON FUNCTION configure_product_delegation(uuid,text,text,jsonb,boolean,bigint) FROM PUBLIC;
REVOKE ALL ON FUNCTION disable_product_delegation(uuid) FROM PUBLIC;

ALTER TABLE credit_packs
  ADD COLUMN version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
  ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now();

-- +goose Down
ALTER TABLE credit_packs DROP COLUMN updated_at, DROP COLUMN version;
DELETE FROM delegation_client_profiles profile
USING product_delegation_configurations configuration, product_delegation_actors actor
WHERE actor.product_id = configuration.product_id
  AND actor.environment = configuration.environment
  AND profile.authorizer_client_id = configuration.authorizer_client_id
  AND profile.actor_client_id = actor.actor_client_id;
DROP FUNCTION disable_product_delegation(uuid);
DROP FUNCTION configure_product_delegation(uuid,text,text,jsonb,boolean,bigint);
DROP TABLE product_delegation_configuration_events;
DROP INDEX product_delegation_actors_client_environment_idx;
DROP TABLE product_delegation_actors;
DROP INDEX product_delegation_configurations_authorizer_environment_idx;
DROP TABLE product_delegation_configurations;
