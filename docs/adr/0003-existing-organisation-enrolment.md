# ADR0003: Enrol existing organisations without replacing their identity

Status: accepted for implementation. Owner: #12; consumer: helivanta#1135.

## Context and budget

Existing Helivanta tenants already have Zitadel organisations, tenant UUID
metadata and product grants, while the onboarding registry is empty. Creating
replacement organisations would break existing records and access. An entered
identifier alone cannot establish a registry entitlement.

Assume at most 100 existing tenants initially, 1,000 after twelve months and
4,000 after thirty-six months. Administrator-driven enrolment is write-only,
below one request/second, with a maximum 2 KiB request and five bounded source
reads per attempt. Registry and audit growth is below 10 MiB at that volume
before backups. Reuse PostgreSQL and the existing source client; no new service,
cache or fixed infrastructure cost. Target 99.9% availability and p99 below one
second, with a one-second source deadline; these targets need measurement.

## Decision and trust boundary

Add `POST /v1/orgs/enrolments`, available only to a platform administrator.
Products, customer administrators and platform operators cannot bootstrap their
own entitlement. The administrator supplies the expected tenant UUID, source
organisation ID, registered product key, display name and an attributable reason.
These are untrusted claims until the onboarding service verifies the live source
organisation, exact metadata UUID, active product project and organisation grant.
The display name is an administrative label, never identity evidence.

The live proof must be current, active and match every requested binding. Missing
facts deny; timeouts, malformed facts and source uncertainty fail unavailable.
No source mutation, credential, user role, domain or licence is created. The
product machine remains separately provisioned through the owning identity and
secret-delivery process. Enrolment conveys no legal or trading approval.

After source I/O, one short PostgreSQL transaction locks and rechecks the exact
active product configuration, preserves the organisation UUID/source mapping,
creates only a missing entitlement with no inferred roles, and appends the
hash-chained audit with reason and source revisions. Inactive organisations,
revoked entitlements and conflicting mappings are conflicts; enrolment never
reactivates them. No database lock spans source I/O. The proof is a point-in-time
observation; product identity reads still revalidate the live source afterwards.

Organisation UUID and `(org_id, product_key)` uniqueness make concurrent/retried
enrolment produce one registry change and one successful audit. A repeat checks
the source again but does not overwrite private profile fields or emit a second
success event. The existing request idempotency middleware remains available.
There is no broker event or saga because this operation writes only its own
database after read-only source verification.

## Recovery and verification

No migration or backfill. An audit failure rolls the registry transaction back;
a crash after commit is recoverable by repeating the same binding. Rollback
removes the route without deleting enrolled tenants or changing Zitadel. Existing
revocation workflows remain authoritative. No direct registry SQL, regenerated
tenant IDs or product-held IAM administrator credentials are acceptable substitutes.

Verify real PostgreSQL API behaviour, concurrent retries, conflicting bindings,
revoked/inactive state, current-product rechecks, source failures and role denial
before provider I/O. Reuse the live Zitadel verifier protocol coverage. Run full
race/coverage, lint, vet, build and security gates before release. Production
enrolment and positive product-machine acceptance require separate evidence.
