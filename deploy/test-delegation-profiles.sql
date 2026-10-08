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
  ('daybook-billmesh-authorizer-test', 'daybook-catalogue-test', 'billmesh.catalogue', 'catalogue', 'service', 'daybook', 'production', 1, true),
  ('daybook-billmesh-authorizer-test', 'daybook-billing-test', 'billmesh.billing', 'billing', 'user', 'daybook', 'production', 1, true),
  ('daybook-billmesh-authorizer-test', 'daybook-billing-service-test', 'billmesh.billing', 'billing', 'service', 'daybook', 'production', 1, true),
  ('daybook-billmesh-authorizer-test', 'daybook-runtime-test', 'billmesh.runtime', 'runtime', 'service', 'daybook', 'production', 1, true),
  ('daybook-billmesh-authorizer-test', 'daybook-billing-staging-test', 'billmesh.billing', 'billing', 'user', 'daybook', 'staging', 1, true),
  ('taskmesh-billmesh-authorizer-test', 'taskmesh-catalogue-test', 'billmesh.catalogue', 'catalogue', 'service', 'taskmesh', 'production', 1, true),
  ('taskmesh-billmesh-authorizer-test', 'taskmesh-billing-test', 'billmesh.billing', 'billing', 'user', 'taskmesh', 'production', 1, true),
  ('taskmesh-billmesh-authorizer-test', 'taskmesh-billing-service-test', 'billmesh.billing', 'billing', 'service', 'taskmesh', 'production', 1, true),
  ('taskmesh-billmesh-authorizer-test', 'taskmesh-runtime-test', 'billmesh.runtime', 'runtime', 'service', 'taskmesh', 'production', 1, true),
  ('taskmesh-billmesh-authorizer-test', 'taskmesh-billing-staging-test', 'billmesh.billing', 'billing', 'user', 'taskmesh', 'staging', 1, true),
  ('billmesh-authorizer-test', 'billmesh-global-admin-test', 'billmesh.admin', 'admin', 'service', '*', '*', 1, true),
  ('billmesh-authorizer-test', 'billmesh-global-admin-user-test', 'billmesh.admin', 'admin', 'user', '*', '*', 1, true)
ON CONFLICT (authorizer_client_id, actor_client_id) DO UPDATE SET
  scope = EXCLUDED.scope,
  client_type = EXCLUDED.client_type,
  actor_type = EXCLUDED.actor_type,
  application = EXCLUDED.application,
  environment = EXCLUDED.environment,
  context_profile_version = EXCLUDED.context_profile_version,
  enabled = EXCLUDED.enabled;
