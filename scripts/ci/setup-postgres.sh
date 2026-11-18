#!/usr/bin/env bash
set -euo pipefail
port=${PGPORT:-5432}
[[ "$port" =~ ^[0-9]+$ ]]
((port > 0 && port < 65536))
schema=$(mktemp)
trap 'rm -f "$schema"' EXIT
curl --fail --silent --show-error --location \
  https://raw.githubusercontent.com/tesserix/tesserix-k8s/d8e2ddee123bafe38f62b9ba81497a4ed4bfcfd0/charts/apps/db-schema-bootstrap/schemas/global/global/onboarding_db.sql \
  --output "$schema"
printf '%s  %s\n' cefe10aeac3a460551af6d67c04ae2d967fa1d1dee2a72dc50e8665c1dd0a55b "$schema" | shasum -a 256 -c >&2
PGPASSWORD=local psql -h 127.0.0.1 -p "$port" -U local -d app_test -v ON_ERROR_STOP=1 >&2 <<'SQL'
DO $$ BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'onboarding') THEN
    CREATE ROLE onboarding NOLOGIN;
  END IF;
END $$;
SQL
PGPASSWORD=local psql -h 127.0.0.1 -p "$port" -U local -d app_test -v ON_ERROR_STOP=1 -f "$schema" >&2
printf 'ONBOARDING_TEST_DSN=postgres://local:local@127.0.0.1:%s/app_test\n' "$port"

retained_schema=$(mktemp "${RUNNER_TEMP:-/tmp}/onboarding-owner-schema.XXXXXX.sql")
cp "$schema" "$retained_schema"
printf 'ONBOARDING_TEST_SCHEMA=%s\n' "$retained_schema"
