#!/usr/bin/env bash
set -euo pipefail
port=${PGPORT:-5432}
[[ "$port" =~ ^[0-9]+$ ]]
((port > 0 && port < 65536))
schema=$(mktemp)
trap 'rm -f "$schema"' EXIT
curl --fail --silent --show-error --location \
  https://raw.githubusercontent.com/tesserix/tesserix-k8s/95a2363928ef79d162e358332b79049aa51d1589/charts/apps/db-schema-bootstrap/schemas/global/global/onboarding_db.sql \
  --output "$schema"
printf '%s  %s\n' c598be7b089b5ba977614b8f40aec9038c5d8f1f7fe9932d87eed40b565379f7 "$schema" | shasum -a 256 -c >&2
PGPASSWORD=local psql -h 127.0.0.1 -p "$port" -U local -d app_test -v ON_ERROR_STOP=1 >&2 <<'SQL'
DO $$ BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'onboarding') THEN
    CREATE ROLE onboarding NOLOGIN;
  END IF;
END $$;
SQL
PGPASSWORD=local psql -h 127.0.0.1 -p "$port" -U local -d app_test -v ON_ERROR_STOP=1 -f "$schema" >&2
printf 'ONBOARDING_TEST_DSN=postgres://local:local@127.0.0.1:%s/app_test\n' "$port"
