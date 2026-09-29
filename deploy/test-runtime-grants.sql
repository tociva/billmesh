-- Test stack only. Migrations use the schema owner; API and worker use this DML-only role.
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'billmesh_runtime') THEN
    CREATE ROLE billmesh_runtime LOGIN PASSWORD 'runtimepassword';
  END IF;
END $$;

\set runtime_role billmesh_runtime
\set migration_role billmesh
\set database_name billmesh_e2e
\i /scripts/runtime-grants.sql
