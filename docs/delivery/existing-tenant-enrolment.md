# Existing tenant enrolment — local acceptance

Owning issue: #12. Exact-grant lookup defect: #13. Related Helivanta stories: #1135 and #1136.

The platform-admin API adopts a verified existing tenant UUID/source binding
without creating replacement identities. It appends the entitlement and success
audit in one PostgreSQL transaction. Retries and concurrent requests preserve one
entitlement and one success event. Display names remain administrative labels;
no user role, licence or bilateral trading permission is inferred.

Observed TDD: `TestExistingOrganisationEnrolmentPreservesIdentityAndAuditsOnce`
returned HTTP 404 before implementation, then passed against the isolated local
PostgreSQL database. Third-party source responses in API tests are controlled
fixtures; these tests do not claim positive production machine acceptance.

Validation with Go 1.27.2:

- `go test -race -p 1 -coverprofile=/tmp/onboarding-enrolment-final-coverage.out ./...`
  passed every API package against the isolated PostgreSQL database. Final total
  statement coverage: 49.3%, above the configured 47% gate.
- Affected API/store/authz race suites passed. Cases cover product/anonymous
  denial before source I/O, inactive/missing/mismatched source facts, conflicting
  UUID/source mappings, revoked/inactive local records, revocation during source
  I/O, changed product configuration, eight concurrent retries, immutable profile
  preservation and audit failure rollback. The scoped identity read after
  enrolment and its subsequent revocation are also exercised.
- `golangci-lint run ./...`: zero issues. `go vet ./...` and `go build ./...`
  passed. `govulncheck ./...`: zero reachable vulnerabilities; one advisory in an
  uncalled required-module path. `gitleaks dir . --redact --no-banner`: no leaks.

No schema migration, production registry write, new identity grant or credential
has been applied. Production source workloads remain on PR #11. The existing
bootstrap identity returned HTTP 403 during read-only project discovery, so
actual project/machine IDs, canonical registration, OpenBao/ESO delivery and
positive machine acceptance remain pending. Do not close #12 or the broader B2B
stories from this local prerequisite alone.

The new console form is available at `/orgs/enrol` through normal authenticated
administration. It exposes only active Zitadel products, preserves submitted
fields on an error, disables repeated submission while pending and validates the
returned binding before showing success. The four new component tests first
failed on the missing component, then passed. All 25 web tests, type checks,
ESLint, formatting and Next.js production build passed on Node 24.11.1. No new
browser session or production enrolment has been manufactured for UI acceptance.

The unchanged local live Zitadel fixture test initially found an active search
result after grant deletion. The deterministic TLS regression reproduced that
acceptance. The exact-grant read now awaits the source projection, checks all
bindings and retains its revision; inactive search state still denies. The TLS
suite and the unchanged live fixture test then passed. Owned local TEST
organisations/project/grant fixtures were cleaned up by the existing test. This
is not positive production machine acceptance or a claim of distributed
serializability.
