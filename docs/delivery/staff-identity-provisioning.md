# Staff identity provisioning implementation checkpoint

Updated: 2026-10-31. Status: draft, incomplete; do not merge or deploy as completed
provisioning. Owner [PR #22](https://github.com/tesserix/onboarding-service/pull/22)
and canonical SQL [PR #1616](https://github.com/tesserix/tesserix-k8s/pull/1616)
are coordinated with Helivanta A01–A12. Exact protected receipt and observation
routes are implemented in the current candidate. Creation submission, human
authorization in Helivanta, recipient verification, membership and employment
activation are not implemented by these endpoints.

## Implemented and tested

The provider protocol creates a human without roles, passwords or a verified
contact override, using frozen organisation/ID facts and correlation metadata.
It never follows redirects or automatically repeats a possibly dispatched
creation. Duplicate fields, incorrect IDs, missing/invalid timestamps, extra
codes, malformed/oversized responses and provider failures remain uncertain.
Private input formatting and provider errors do not disclose contacts or codes.

The owner store atomically prepares immutable input with audit, permits one
permanent dispatch claim, and retains one immutable creation receipt with audit.
Preparation replays preserve the original server-selected provider ID and nonce;
changed actor/input conflicts. Dispatch rechecks current product, machine,
organisation, source mapping and entitlement under database locks. No transaction
or lock spans provider I/O. Receipts require the original command and dispatch,
preserve source timestamp nanoseconds, and cannot be silently replaced.

Retaining an already-dispatched provider outcome after entitlement withdrawal
does not restore access or permit another dispatch. Its later disclosure and
eligibility still require current service/product/human authorization. No
operator recovery endpoint is implemented by these store methods.

The [API contract](../api/staff-identity-provisioning.md) records the new GET/POST
routes. They require a current product machine matching the explicit configured
canonical key, its active tenant entitlement, fresh owner identity proof and the
original initiating actor/command binding. `hms` is only a fixture, not a default
or an alias for production registration. Owner issue #12 remains open.

The provider observer binds account/organisation/contact/nonce and exact birth
time, rereading the user after metadata. Current owner scope is rechecked after
provider I/O and again under locks before disclosure. An uncertain creation can
be reconciled, but never dispatched again. GET releases only retained facts;
POST reports current state separately and does not verify email or authenticate
the recipient. Current-state observations bypass the HTTP replay cache.

Canonical SQL is pinned to `tesserix-k8s` revision
`18d42798e92136b74bdf5ece8c4142c7890fd8d7`; SHA-256
`706e027e603cd8b0a1320128b1c48da53106c90c421bcf359ec51a0aa7407954`.
CI downloads that exact source and checks the hash; there is no copied schema
master here. SQL was applied repeatedly only to an isolated PostgreSQL 16
fixture. The application cannot UPDATE/DELETE/TRUNCATE its three ledger tables;
tenant/product deletion cannot erase their history. There is no destructive
down migration: retain the facts on application rollback.

## Verification

Tests first reproduced missing public methods and absent canonical relations.
Provider regressions also reproduced acceptance of duplicate proof fields and
dispatch of control-character names before their fixes. Real PostgreSQL tests
cover exact/conflicting preparation, eight concurrent claimants with one winner,
receipt replay, application-role rewrite denial, scope/source changes and all
three audit rollback boundaries. A lock-wait test observes `pg_blocking_pids`,
withdraws entitlement before releasing its blocker, and verifies no claim.

Commands run from `apps/api`, using Go 1.27.3 and a disposable
`ONBOARDING_TEST_DSN`:

```sh
go test -race -p 1 -covermode=atomic -coverprofile=/tmp/onboarding-staff-ledger-coverage.out ./...
go test -race -cover ./internal/store ./internal/zitadel -count=1
go vet ./...
go build ./...
golangci-lint run ./internal/store/... ./internal/zitadel/...
go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
```

The full suite passed at 52.7% overall coverage. Final affected suites passed at
56.2% store / 81.1% provider coverage; vet/build/lint passed with zero lint issues.
The symbol scan reports no affected code vulnerabilities; it separately reports
three vulnerabilities in required modules with no observed call paths. This is
not a claim that every transitive module is free of advisories.

The four Python delivery tests, workflow actionlint, shell syntax, formatting and
diff checks passed. A local Linux/arm64 Docker build using the actual updated
Dockerfile succeeded. Schema repository Helm lint and its four onboarding schema
tests passed. At 100,000 synthetic commands the exact scoped lookup used an
index scan with four shared-buffer hits, 0.066 ms local execution; those planning
rows were rolled back. This is not API latency acceptance.

Owner CI initially failed before jobs because the old shared workflow revision
was unresolved. Its replacement on current shared main has the identical Go
workflow blob. Subsequent CI found GO-2026-6809 in Go 1.27.2 and unavailable base
image digests. Module/workspace/CI/container now consistently use Go 1.27.3 and
currently retrievable digest-pinned shared images; no scan exemption was added.
Record the final current-head CI separately from these local results.

The next increment reproduced and fixed conflicting JSON source fields being
accepted as active, a denied request held beyond the four-second watchdog by an
unbounded audit lock, a generic HTTP cached observation surviving both changed
verification and entitlement withdrawal, and valid 200-character Unicode names
being rejected by a byte limit. Protected read tests cover current actor/scope,
audit rollback and private response minimization. Source proof tests cover
version/state changes, contact/owner/nonce mismatch, retained birth mismatch,
inactive/unverified/missing/deleted accounts, redirects and cancellation.
Middleware fault/retry tests use real PostgreSQL; the IdP is an external fixture.
No real recipient contact-verification UI acceptance follows from those tests.

At `87fc47f`, owner CI `41052581748` passed all applicable gates, including image
build and vulnerability audit. Schema PR #1616 passed all applicable CI gates at
`18d42798`. These baselines do not establish the next candidate's CI.

The final local full owner race suite passed at 53.7% overall coverage, including
API 61.7%, store 58.0%, provider 83.7% and configuration 93.8%. Full vet/build/lint
passed with zero issues; Python delivery tests, actionlint, shell syntax, redacted
Gitleaks and a fresh Linux/arm64 image build passed. The dependency symbol scan
again found no affected-code vulnerabilities, with the same three uncalled
required-module advisories.

Additional primary-source review found that v4.15.3
`internal/api/grpc/user/v2/user_query.go:GetUserByID` requests triggering through
`GetUserByIDWithPermission(ctx, true, ...)`, while management metadata reads call
`GetUserMetadataByKey(ctx, true, ...)`. The metadata query helper logs trigger
errors and then executes its SQL read; the user path calls a void trigger helper
before its query. Matching row versions across reads is therefore not proof that
projection synchronization succeeded. This is an unclosed provider-failure
acceptance gate at the earlier checkpoint, superseded by the reproduced fault
and candidate correction below. No membership/permission activation exists in
these routes.

### Actual projection-failure reproduction and correction — 2026-10-31

A new isolated provider uses the exact v4.15.3 digest
`2356d646340724b3d843c024d589314adb9b54dbd6452ebc34008b011e023eff`, separate
PostgreSQL and loopback port 32880. No existing dev/production provider or
credential was changed. A synthetic active/verified fixture was created using
an explicit setup override; this is not recipient-verification acceptance.

A user-specific UPDATE trigger raised a database exception while preserving
SELECT. Provider deactivation committed at sequence 4, but both following user
reads returned HTTP 200 active at sequence 2 and metadata remained at sequence
3. The actual owner method returned active/verified without error in 102 ms.
The automated stale-deactivation regression failed before correction.

The correction adds an exact, 16-type filtered event-store read, with
`AwaitOpenTransactions` verified in v4.15.3 `admin/event.go`, within the existing
two-second deadline. It uses existing owner `events.read`; no credential/role
expansion occurred. The same live fault then returned unavailable in 106 ms.
After removing the fault and issuing a real synthetic reactivation (sequence
5), the owner returned active/verified at revision 5 in 86 ms. An unrelated
metadata update at sequence 6 remained accepted in 79 ms, preserving nonce
revision 3. A separate metadata UPDATE fault let nonce replacement commit at
sequence 7 while its projection retained the original nonce/revision 3; the
corrected owner denied it in 110 ms. Removing that fault and restoring the
synthetic binding through a provider command (sequence 8) restored successful
observation in 79 ms. Fault triggers are removed; immutable provider event
history and private fixture artifacts remain. These timings are individual
local observations, not p99 or production SLO evidence.

Extended repeated-failure testing then invalidated the first revision-only
correction. After 15 failing projection reads, deactivation sequence 14 was
skipped; an unrelated profile edit advanced the user to revision 15 while
retaining active state. The initial event guard returned active/verified in
104 ms. This is an actual reproduction, consistent with v4.15.3
`handler/v2/failed_event.go:handleFailedStmt` continuing at its failure limit.
The final correction additionally compares latest authoritative state,
email-change and email-verification events through three exact limit-one
queries. Its skipped-deactivation regression failed before the correction, and
the same live source then denied proof in 135 ms. A real synthetic reactivation
at sequence 16 restored successful observation in 227 ms. No hospital membership
or employment was activated by these provider-only tests.

Regressions also cover skipped contact changes/verification ordering despite
later profile revisions, malformed/foreign latest-field proofs, unreflected
state/email/nonce mutations, unrelated
metadata, initialization without row-revision advancement, foreign/ambiguous
event scope, invalid/duplicate/descending sequences, null/duplicate JSON,
saturation, denied/unavailable event stores, redirect rejection and the exact
bounded filtered request. Current organisation/registry authority, real
recipient setup, full creation/recovery and consumer acceptance remain open.

## Remaining acceptance

Final local correction validation passed: full owner race/coverage suite with
the actual isolated PostgreSQL fixture (54.6% overall, provider 85.2%), full vet,
build and golangci-lint (zero issues), diff/format checks and redacted Gitleaks.
The final Linux/arm64 image build passed as
`helivanta-staff-onboarding-current-proof:local`. Four Python delivery tests
also passed. Earlier checkpoint CI 41066660408 passed all applicable gates at
`e3dc7e5`; this does not establish the new correction's CI.

Commands run from `apps/api/` include:

```sh
ONBOARDING_TEST_DSN='postgres://postgres@127.0.0.1:32768/postgres?sslmode=disable' \
  mise exec go@1.27.3 -- go test -race -p 1 -covermode=atomic \
  -coverprofile=/tmp/onboarding-staff-current-proof-final-coverage.out ./...
mise exec go@1.27.3 -- go vet ./...
mise exec go@1.27.3 -- go build ./...
mise exec -- golangci-lint run ./...
```

These are owner/provider acceptance checks; no new normal-role UI journey,
consumer activation, schema deployment or production release is claimed.

- Creation submission with duplicate/contact reservation, current source and
  actor boundaries, at-most-once dispatch and full uncertain-outcome handling.
- Final review/CI and consumer acceptance of the event-store-backed provider
  freshness correction, including the configured provider version/capability
  and current organisation/registry proof; projected rows alone are insufficient.
- Current human authority in Helivanta before and after owner requests, its
  canonical client/Global API contracts and exact account/tenant UI binding.
- Protected bounded command history and attributable operator recovery,
  including replacement/conflict/orphan handling. A missing provider user is
  never a reason to send the same create again.
- Real recipient contact verification and account setup through configured
  provider delivery and normal UI. Synthetic `isVerified` fixtures are not proof.
- Reviewed existing-identity resolution and historical Staff/link/grant
  classification, followed by least-privilege Helivanta membership and independent
  current-person linking/review; no creation receipt activates operational access.
- Full owner/consumer failure, restart, race, isolation and UI acceptance, then
  exact approved SQL/source rollout and deployed acceptance. Do not close
  Helivanta A01/A02/A11 or tag a release from protocol/store tests alone.
