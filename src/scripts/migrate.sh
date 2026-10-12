#!/usr/bin/env sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
DATABASE_URL=${NEXUSAUTHORIZATION_DATABASE_URL:-postgres://postgres:postgres@localhost:5432/permission_center?sslmode=disable}
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -c '
CREATE TABLE IF NOT EXISTS schema_migrations (
  name TEXT PRIMARY KEY,
  applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);'

for migration in "$ROOT"/migrations/*.sql; do
	name=$(basename "$migration")
	applied=$(psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -Atc "SELECT 1 FROM schema_migrations WHERE name = '$name'")
	if [ "$applied" = "1" ]; then
		continue
	fi
	psql "$DATABASE_URL" -v ON_ERROR_STOP=1 --single-transaction -f "$migration"
	psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -c "INSERT INTO schema_migrations (name) VALUES ('$name')"
done
