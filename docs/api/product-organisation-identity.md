# Live product organisation identity

`GET /v1/orgs/{canonical-tenant-uuid}/identity`

This endpoint serves product machine principals only. The authenticated machine
subject selects the product from the registry. The caller cannot override the
product, project, organisation, or source credentials through query parameters.
Human and other-product callers cannot enumerate tenant identities.

The service checks the organisation and active product entitlement in PostgreSQL,
reads the organisation, canonical tenant metadata, project, and exact project
grant from Zitadel, then rechecks registry authorization before returning. It
holds no database transaction or lock across source I/O. A changed entitlement
during verification is denied. A source outage never falls back to stored state.

Example response using synthetic opaque identifiers:

```json
{
  "schema_version": 1,
  "tenant_id": "11111111-1111-4111-8111-111111111111",
  "product_key": "configured-product-key",
  "organization_id": "source-org-id",
  "project_id": "source-project-id",
  "grant_id": "source-grant-id",
  "state": "active",
  "verified_at": "2026-10-17T00:00:00Z",
  "source_revisions": {
    "organization": "101",
    "metadata": "102",
    "project": "103",
    "grant": "104"
  }
}
```

Sequence values are decimal **strings**, preserving source precision. No legal
names, contacts, addresses, billing details, roles, or credentials are included.
Responses include `Cache-Control: no-store`. Consumers must make a fresh read
for eligibility-sensitive transitions and reject unknown schema, state, missing
provenance, stale observations, or mismatched tenant and product bindings.

| Status | Meaning | Consumer behavior |
| --- | --- | --- |
| 200, active | All four source facts are active and bound to the exact tenant/product | Continue remaining business authorization checks |
| 200, inactive | A known source fact is explicitly inactive | Deny new network eligibility |
| 400 | Invalid tenant UUID or query overrides | Fix the request; do not retry unchanged |
| 401 | Missing or invalid authentication | Obtain the dedicated product credential |
| 404 | Unauthorized, unknown, revoked, or absent identity | Deny without disclosing another product's records |
| 503 | Source unavailable, malformed, ambiguous, or mismatched | Fail closed; allow an explicit later retry |

The outer verification budget is one second; the four parallel source reads
share a 750 ms deadline. Redirects are rejected and source bodies are limited to
64 KiB. There is no automatic application retry, eligibility cache, or new data
store. Consumers should also bound their deadlines and error output.

This is a point-in-time observation, not a distributed lock or a licence to
trade. It does not establish bilateral consent, premises/licence verification,
country/category eligibility, or access to clinical records. Those checks belong
to the product at its own transaction boundary. Source credentials remain inside
onboarding-service; downstream products must never receive its administrative
Zitadel credential.

## Local verification

Run the regular suite with `ONBOARDING_TEST_DSN` pointing to an isolated local
PostgreSQL database, never production:

```sh
GOTOOLCHAIN=go1.27.2 GOFLAGS=-p=1 go -C apps/api test -race -coverprofile=coverage.out ./...
python3 -m unittest discover -s tests -v
```

The optional live fixture test additionally requires a local Zitadel instance
and a credential file supplied outside Git:

```sh
ONBOARDING_LIVE_IDENTITY_URL=http://auth.tesserix.localhost:20080 \
ONBOARDING_LIVE_IDENTITY_PAT_FILE=/path/outside/git/local-seed.pat \
GOTOOLCHAIN=go1.27.2 GOFLAGS=-p=1 \
go -C apps/api test -race -tags=identityintegration ./internal/zitadel \
  -run TestLiveOrganizationIdentity -count=1 -v -timeout=2m
```

It refuses other origins, creates generated TEST organisations/project/grant,
checks their ownership before use or cleanup, and removes its fixtures. It tests
metadata changes and organisation, project, and grant suspension/revocation.

## Release and recovery

No schema migration is needed. The additive route has no effect on existing
callers. Deploy through the existing shared CI/image gates and GitOps delivery;
verify scoped machine authentication and live source access before enabling any
consumer. No consumer credential or IAM expansion is included in this change.
Rolling back to the previous API image removes this route; consumers must remain
fail closed rather than fall back to the stored organisation profile.
