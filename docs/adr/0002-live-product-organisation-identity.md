# ADR 0002: Live organisation identity for product network links

Status: proposed implementation. Consumer: `tesserix/helivanta#1136`. This records platform identity and entitlement, not legal registration, licence verification or permission to trade.

## Context and budget

PharmaConnect needs to bind a consenting buyer/seller to the existing platform tenant, without treating a supplier's typed tenant ID or private declaration as canonical identity. `GET /v1/orgs/:id` currently returns the authorized database projection; it does not revalidate the live Zitadel org, tenant metadata or project grant. That projection cannot be used as current trading eligibility.

Planning assumptions: 100 participating organisations / 300 locations, 1,000 orders/day and two identity observations/order = 2,000 reads/day (0.023 average RPS). Allow 20 peak reads/s for bursts and future consumers, each <=1 KiB response and four parallel, bounded discovery reads followed by one exact grant read. This is >800x the daily average, not a measured limit. Read:write ratio is 100:0 for this endpoint. No new persistent rows or datastore; if consumers retain a 512-byte proof/order, 365,000 orders/year use ~180 MiB/year before indexes, ~1.3 GiB cumulative over three years with 2x annual growth. Target 99.9% availability and p99 300ms; the source deadline is 750ms and timeout denies. These are proposed targets requiring measurement. Reuse the current API/Postgres/Zitadel deployment; no additional fixed infrastructure cost is expected, and existing capacity must be measured before scaling.

## Contract and decision

Add **`GET /v1/orgs/:id/identity`**, restricted to an authenticated product machine principal and its current `org_products` entitlement. No product key, identity provider ID or privileged token comes from the request. Platform/org-user console routes keep their existing contracts; humans cannot use this product correctness endpoint.

After product/organisation authorization, load the product and tenant projection to resolve source identifiers. Using only the onboarding service's existing identity client, read the current org, its `tenant_id` metadata, the product project and the exact project grant. Use the established management/admin API versions and verified official Zitadel protobuf HTTP paths. All reads share a bounded context, reject redirects, bound response size and never log provider response bodies. No provider I/O or database transaction lock overlaps.

The live metadata UUID must equal the requested internal platform tenant UUID. Source organisation/project/grant identifiers, grant owner and grant target must match the authorized projection. Missing, duplicate, malformed or unknown source facts do not establish identity. Known inactive organisation/project/grant state returns an inactive observation. Missing identity/grant returns 404; source failure, mismatch or unknown state returns generic 503. Scope denial always returns the existing indistinguishable 404 before provider I/O.

Grant search alone is insufficient: a local live test observed an active search
result immediately after deletion. Resolve the discovered grant through
`GET /management/v1/projects/{project}/grants/{grant}` before constructing the
observation. Zitadel v4.15.3's `GetProjectGrantByID` calls
`ProjectGrantByID(ctx, true, ...)`, which awaits the grant projection; search does
not. Check the exact owner/project/target/ID again and reject a revision older
than the search result. Use the exact lookup revision as provenance. Explicit
inactive state in either read prevents an active observation. All five reads
share the existing source deadline and response bounds; there is no retry or
fallback to search. This fixes the observed lag case; the source remains a
point-in-time authority and does not provide a distributed lock or a guarantee
against a later revocation. Related defect: #13.

The minimized response contains schema version, canonical tenant UUID, opaque Zitadel org/project/grant IDs, the caller's product key, active/inactive state, verification time and source revision sequences as decimal strings. Names, contacts, billing, domains, labels, roles, credentials and source metadata values are absent. Set `Cache-Control: no-store`; this endpoint has no eligibility cache, fallback projection or automatic approval. A verified identity never overrides the consumer's current OpenFGA membership, explicit organisation/location grants, country policy, licences, bilateral consent, stock or credit.

This is a bounded point-in-time source observation, not a distributed lock or guarantee against subsequent revocation. Consumers recheck before new commitments, store provenance in their own atomic audit/outbox transaction, and retain pending/failed states during source outages. Their relationship/order saga must define concurrent revocation and existing-obligation behavior; stale directory/UI data cannot authorize a new commitment.

## Recovery and alternatives

No migration/backfill or credential rotation is needed for the endpoint. Rollback removes the new route; callers fail closed on 404/unavailability. Products never gain the iam-admin credential or direct access to onboarding_db/Zitadel management. A dedicated consumer credential, network policy and rollout require their own review and specific production authorization if not already provisioned. Existing APIs and grants remain unchanged.

Reject using a typed external tenant ID, private supplier profile or cached projection as verified identity. Reject a second tenant/org registry in PharmaConnect. Reject granting the product Zitadel management access. The existing scoped onboarding authority with a minimized live observation is the smallest boundary that preserves source ownership.

Verification: real HTTPS protocol tests for scoped headers/filters, active/inactive/missing/mismatched facts, response bounds, redirects and cancellation; real PostgreSQL API tests for own/foreign/revoked product grants and no provider call before authorization. Run Go race, lint/vet/build, security and delivery checks with Go 1.27.2 before proposing release. No B2B story closes on this prerequisite alone.
