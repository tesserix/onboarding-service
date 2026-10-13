# onboarding-service

Platform onboarding API behind [onboard.tesserix.app](https://onboard.tesserix.app):
creates organizations (tenants) in Zitadel, grants them products, verifies their
domains, wires their SSO providers, and records every privileged action in an
append-only ledger.

Design and API surface: [`tesserix-k8s/docs/onboarding-api.md`](https://github.com/tesserix/tesserix-k8s/blob/main/docs/onboarding-api.md).

## Layout

```
apps/api/          Go 1.26 + Gin + pgx API
  cmd/server       wiring only
  internal/zitadel Zitadel management client (sole holder of the iam-admin PAT)
  internal/store   onboarding_db projection and the audit ledger
  internal/authz   default-deny policy
  internal/onboarding orchestration
  internal/api     HTTP surface, session/CSRF, idempotency, rate limiting
apps/web/          Next.js 16 console, served same-origin with the API
deploy/            container builds
```

The console holds no credential: it is served from the same origin as the API,
so the session is a first-party HttpOnly cookie and the only thing the browser
code touches is the companion CSRF value.

The database schema is **not** in this repo — it lives in
`tesserix-k8s/charts/apps/db-schema-bootstrap/schemas/global/global/onboarding_db.sql`
and is applied by the `db-schema-bootstrap` CronJob.

## Running the tests

Tests use a real Postgres, not a mocked repository:

```bash
export ONBOARDING_TEST_DSN='postgres://onboarding@127.0.0.1:5432/onboarding_db'
psql "$ONBOARDING_TEST_DSN" -f ../tesserix-k8s/charts/apps/db-schema-bootstrap/schemas/global/global/onboarding_db.sql
cd apps/api && go test -race ./...
```

The console:

```bash
cd apps/web && pnpm install && pnpm typecheck && pnpm test && pnpm build
```

## Configuration

| Variable | Required | Default |
|---|---|---|
| `DATABASE_URL` | yes | — |
| `ZITADEL_PAT` | yes | — |
| `PLATFORM_ORG_ID` | yes | — |
| `SESSION_KEY` | yes (32 bytes, raw or base64) | — |
| `ZITADEL_ISSUER` | no | `https://auth.tesserix.app` |
| `ZITADEL_JWKS_URL` | no | `<issuer>/oauth/v2/keys` |
| `TOKEN_AUDIENCE` | no | `onboarding-api` |
| `CONSOLE_CLIENT_ID` | no | — |
| `CONSOLE_URL` | no | `https://onboard.tesserix.app` |
| `OIDC_REDIRECT_URI` | no | `<console url>/api/auth/callback` |
| `SECURE_COOKIES` | no | `true` |
| `DB_MAX_CONNS` | no | `10` |
| `PORT` | no | `8080` |

## Production delivery

The API (`apps/api`) and console (`apps/web`) use the shared Tesserix base images
and reusable quality/container workflows. Kargo promotes verified `deploy` commits
to Argo CD. See [the delivery contract](docs/adr/0001-standard-delivery.md) for image
ownership, gates, recovery and rollout requirements.

Web checks: `pnpm --dir apps/web format:check`, `lint`, `test`, `check-types`, `build`.
Release contracts: `python3 -m unittest discover -s tests -v`.
