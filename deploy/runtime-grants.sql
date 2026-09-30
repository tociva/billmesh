-- Run as the migration/schema owner after creating the runtime role.
-- Pass runtime_role, migration_role, and database_name via psql -v.
-- The API and worker use runtime_role; migrations use migration_role.
GRANT CONNECT ON DATABASE :"database_name" TO :"runtime_role";
GRANT USAGE ON SCHEMA billmesh TO :"runtime_role";
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA billmesh TO :"runtime_role";
GRANT USAGE, SELECT, UPDATE ON ALL SEQUENCES IN SCHEMA billmesh TO :"runtime_role";
ALTER DEFAULT PRIVILEGES FOR ROLE :"migration_role" IN SCHEMA billmesh
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO :"runtime_role";
ALTER DEFAULT PRIVILEGES FOR ROLE :"migration_role" IN SCHEMA billmesh
  GRANT USAGE, SELECT, UPDATE ON SEQUENCES TO :"runtime_role";
ALTER ROLE :"runtime_role" IN DATABASE :"database_name" SET search_path = billmesh;
