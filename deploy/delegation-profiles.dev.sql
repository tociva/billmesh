INSERT INTO billmesh.delegation_client_profiles (
  authorizer_client_id,
  actor_client_id,
  scope,
  client_type,
  actor_type,
  application,
  environment,
  context_profile_version,
  enabled
) VALUES
  ('daybook-billmesh-authorizer-dev', 'daybook-billmesh-catalogue-dev', 'billmesh.catalogue', 'catalogue', 'service', 'daybook', 'development', 1, true),
  ('daybook-billmesh-authorizer-dev', 'daybook-billmesh-billing-user-dev', 'billmesh.billing', 'billing', 'user', 'daybook', 'development', 1, true),
  ('daybook-billmesh-authorizer-dev', 'daybook-billmesh-billing-service-dev', 'billmesh.billing', 'billing', 'service', 'daybook', 'development', 1, true),
  ('daybook-billmesh-authorizer-dev', 'daybook-billmesh-runtime-dev', 'billmesh.runtime', 'runtime', 'service', 'daybook', 'development', 1, true)
ON CONFLICT (authorizer_client_id, actor_client_id) DO UPDATE SET
  scope = EXCLUDED.scope,
  client_type = EXCLUDED.client_type,
  actor_type = EXCLUDED.actor_type,
  application = EXCLUDED.application,
  environment = EXCLUDED.environment,
  context_profile_version = EXCLUDED.context_profile_version,
  enabled = EXCLUDED.enabled;
