# Identity provisioning API

The operator UI is maintained in
[`tesserix-home/apps/console`](https://github.com/tesserix/tesserix-home/tree/main/apps/console)
at [console.tesserix.app/platform/provisioning](https://console.tesserix.app/platform/provisioning).
It covers product registration, tenant creation/adoption, Helivanta retained
onboarding, organization configuration and identity audit. Signup funnel reporting
is a separate console surface.

This repository owns the private provisioning API, `onboarding_db` projection and
append-only audit ledger. Product machines use `https://console.tesserix.app/v1/*`
with their existing product-scoped OAuth tokens. The console uses a same-origin
BFF and forwards the human operator's server-held access token. No browser receives
an administrative API credential. Operator/registry workloads retain the existing
private Kubernetes service and audience-bound activation contracts.

## Layout and ownership

- `apps/api`: Go 1.27.3, Gin and pgx; Zitadel reconciliation and authorization.
- `deploy/Dockerfile.api`: digest-pinned builder and non-root runtime.
- `tests`: API-only delivery contracts; product/database behavior tests live in Go.
- Database schema: `tesserix-k8s/charts/apps/db-schema-bootstrap/schemas/global/global/onboarding_db.sql`.

The standalone web source, web image and web quality/release job are retired.
Kargo continues to promote the retained API through Argo CD. Retirement preserves
customer records, identities, private claims, OpenBao credentials and held GCS
configuration history. Do not remove the database or recovery sources with the UI.

## Tests

Use a disposable PostgreSQL initialized with the owned schema:

```bash
export ONBOARDING_TEST_DSN='postgres://onboarding@127.0.0.1:5432/onboarding_db'
cd apps/api
go test -p 1 -race ./...
go vet ./...
go build ./...
golangci-lint run
```

Run `python3 -m unittest discover -s tests -v` at the repository root. Console UI,
navigation, session/BFF and browser tests run in `tesserix-home`.

## Configuration

| Variable | Purpose |
| --- | --- |
| `DATABASE_URL` | Required private database connection; never publish it. |
| `ZITADEL_MACHINE_KEY` | Scoped backend provisioning credential; configure exactly one of this or recovery-only `ZITADEL_PAT`. |
| `ZITADEL_ISSUER` / `ZITADEL_JWKS_URL` | Expected Zitadel issuer/JWKS. |
| `TOKEN_AUDIENCE` | Existing product onboarding project audience. |
| `REGISTRY_TOKEN_AUDIENCE` / `REGISTRY_PROJECT_ID` | Existing Agent Registry delegated-token contract. |
| `PLATFORM_ORG_NAME` / `PLATFORM_ORG_ID` | Platform identity org; resolve from name when the ID is absent. |
| `CONSOLE_PROJECT_ID` / `CONSOLE_CLIENT_ID` | Exact platform console project and OAuth client; both required at cutover. |
| `LEGACY_CONSOLE_ENABLED` | Set `false` in production: reject old sealed cookies and remove Google login/callback routes. Compatibility default remains `true` for an explicitly reviewed recovery rollout. |
| `HOSPITAL_PROVISIONING_ENABLED` | Enables private operator-backed hospital claims/activation. |
| `STAFF_IDENTITY_PRODUCT_KEY` | Explicit canonical registered product for protected Staff identity creation/receipts/observations. Empty disables these operations; no `hms` or branding default is inferred. Registration under issue #12 remains required before activation. |
| `HELIVANTA_PROVISIONING_URL` | Reviewed Helivanta workload endpoint. |
| `DB_MAX_CONNS` / `PORT` | Pool sizing / API listener. |

With legacy login disabled, Google client, session-sealing and email-allowlist
configuration is not required or loaded. Do not copy those credentials into the
console. The console authenticates through its own existing Zitadel configuration.
Secrets belong in exact environment/product paths in OpenBao with namespace-bound
identities. Existing transitional bootstrap/recovery sources must remain available
until their separate consumer and recovery checks pass.

See the [hospital provisioning contract](docs/hospital-onboarding.md),
[live product identity contract](docs/api/product-organisation-identity.md), and
[Helivanta step-by-step runbook](https://github.com/tesserix/helivanta/blob/main/docs/delivery/hospital-onboarding.md).
