# Staff identity provisioning implementation checkpoint

Date: 2026-10-30. Status: draft, incomplete; do not merge or deploy as completed
provisioning. Owner [PR #22](https://github.com/tesserix/onboarding-service/pull/22)
and canonical SQL [PR #1616](https://github.com/tesserix/tesserix-k8s/pull/1616)
are coordinated with Helivanta A01–A12. This checkpoint implements no HTTP route,
human authorization, recipient verification, membership or employment action.

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
observation or operator recovery endpoint is implemented by these store methods.

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

## Remaining acceptance

- Authenticated product-machine endpoint with fresh canonical identity proof,
  current registered `hms` product/entitlement checks and current human authority
  enforced in Helivanta before and after the owner request.
- Protected bounded receipt/history reads and exact request/account/tenant
  binding; private contact disclosure must be explicitly authorized and audited.
- Provider observation matching ID, organisation, contact, nonce and creation
  provenance; read-only reconciliation after uncertain dispatch and explicit
  attributable operator recovery. A missing provider user is never a reason to
  send the same create again.
- Real recipient contact verification and account setup through configured
  provider delivery and normal UI. Synthetic `isVerified` fixtures are not proof.
- Reviewed existing-identity resolution and historical Staff/link/grant
  classification, followed by least-privilege Helivanta membership and independent
  current-person linking/review; no creation receipt activates operational access.
- Full owner/consumer failure, restart, race, isolation and UI acceptance, then
  exact approved SQL/source rollout and deployed acceptance. Do not close
  Helivanta A01/A02/A11 or tag a release from protocol/store tests alone.
