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
acceptance gate, not a reproduced live exploit. Add the actual local fault test
and authoritative source revision/watermark proof before using a positive
observation for enrollment or calling identity eligibility complete. The source
still returns minimized observations; no membership/permission activation exists
in these routes.

## Remaining acceptance

- Creation submission with duplicate/contact reservation, current source and
  actor boundaries, at-most-once dispatch and full uncertain-outcome handling.
- Reproduced provider projection-failure behavior and authoritative
  synchronization/revision proof for positive eligibility; do not substitute
  repeated stable query reads for that evidence.
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
