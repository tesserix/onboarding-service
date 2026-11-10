#!/usr/bin/env bash
set -euo pipefail
port=${PGPORT:-5432}
[[ "$port" =~ ^[0-9]+$ ]]
((port > 0 && port < 65536))
schema=$(mktemp)
trap 'rm -f "$schema"' EXIT
curl --fail --silent --show-error --location \
  https://raw.githubusercontent.com/tesserix/tesserix-k8s/c04a794f1ae15e671a238689abddd5b7b6ba037f/charts/apps/db-schema-bootstrap/schemas/global/global/onboarding_db.sql \
  --output "$schema"
printf '%s  %s\n' 6e0cdeb91b7008572e8f4f429dd904da0e053ed5d834158b12a48a6489beaa56 "$schema" | shasum -a 256 -c >&2
PGPASSWORD=local psql -h 127.0.0.1 -p "$port" -U local -d app_test -v ON_ERROR_STOP=1 >&2 <<'SQL'
DO $$ BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'onboarding') THEN
    CREATE ROLE onboarding NOLOGIN;
  END IF;
END $$;
SQL
PGPASSWORD=local psql -h 127.0.0.1 -p "$port" -U local -d app_test -v ON_ERROR_STOP=1 -f "$schema" >&2
printf 'ONBOARDING_TEST_DSN=postgres://local:local@127.0.0.1:%s/app_test\n' "$port"
