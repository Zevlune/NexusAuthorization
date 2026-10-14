#!/usr/bin/env sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
DATABASE_URL=${NEXUSAUTHORIZATION_DATABASE_URL:-postgres://postgres:postgres@localhost:55434/nexus-authorization?sslmode=disable}

if ! command -v psql >/dev/null 2>&1; then
    printf '%s\n' 'PostgreSQL client psql is required to initialize the database.' >&2
    exit 1
fi

# The target database must already exist and be empty. The SQL owns its
# BEGIN/COMMIT transaction, so do not wrap it in --single-transaction.
exec psql "$DATABASE_URL" -X -v ON_ERROR_STOP=1 -f "$ROOT/migrations/001_init.sql"
