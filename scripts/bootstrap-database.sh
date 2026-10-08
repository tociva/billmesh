#!/usr/bin/env bash
set -euo pipefail

project_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
database_name="billmesh"

if (( $# > 0 )); then
  echo "Usage: DB_NAME=billmesh ./scripts/bootstrap-database.sh" >&2
  exit 1
fi

cd "$project_root"
compose=(docker compose)
if [[ -f .env ]]; then
  compose+=(--env-file .env)
fi
compose+=(-f deploy/compose.dev.yml)

"${compose[@]}" up -d --wait postgres mock-external

echo "Recreating local database: $database_name"
"${compose[@]}" exec -T postgres sh -c \
  'exec psql -U "$POSTGRES_USER" -d postgres -v ON_ERROR_STOP=1 -v database_name="$POSTGRES_DB" -v database_user="$POSTGRES_USER" -v database_password="$POSTGRES_PASSWORD"' <<'SQL'
ALTER ROLE :"database_user" WITH PASSWORD :'database_password';
SELECT pg_terminate_backend(pid)
FROM pg_stat_activity
WHERE datname = :'database_name' AND pid <> pg_backend_pid();
DROP DATABASE IF EXISTS :"database_name";
CREATE DATABASE :"database_name";
SQL

echo "Applying Billmesh migrations to schema: billmesh"
env -u DATABASE_URL go run ./cmd/billmesh migrate up

echo "Loading local delegation client profiles"
"${compose[@]}" exec -T postgres \
  psql -U billmesh -d "$database_name" -v ON_ERROR_STOP=1 \
  < deploy/delegation-profiles.dev.sql

echo "Database bootstrap complete for $database_name."
