# Staff identity provisioning implementation checkpoint

Updated: 2026-10-31. Status: draft, incomplete; do not merge or deploy as completed
provisioning. Owner [PR #22](https://github.com/tesserix/onboarding-service/pull/22)
and canonical SQL [PR #1616](https://github.com/tesserix/tesserix-k8s/pull/1616)
are coordinated with Helivanta A01–A12. Exact protected receipt and observation
routes, creation PUT and bounded command activity are implemented in the current candidate. Human
authorization in Helivanta, recipient verification, membership and employment
activation are not implemented by these endpoints.

## Actual recipient contact UI — 2026-10-31

An isolated actual v4.15.3 provider redirected to an absent Login V2 service.
A separate upstream v4.15.3 Login V2 container, pinned by image digest, and a
local-only Mailpit capture unblocked the normal recipient flow. The dedicated
login-client machine has only IAM_LOGIN_CLIENT and a private two-day credential;
original fixture feature/SMTP state was retained before local configuration.
Neither the production provider nor a Staff operational grant was changed.

Browser actions on an existing role-free creation entered its login name,
requested a fresh invitation, read the delivered email and submitted its code.
Fresh provider reads confirmed active state and verified email. The unchanged
owner observer then accepted that current state and the exact retained birth.
Fresh provider membership/user-grant queries both returned zero results. No
password, passkey or verified-contact override was set by the test. Code/token
values and provider bodies remain private and are not consumer responses.

The next visible recipient screen is Choose authentication method. Authenticator
setup and subsequent sign-in are pending; browser credential entry requires a
human handoff. This proves actual local provider delivery/contact UI, with
fixture-supplied registry scope. It does not prove canonical production-source
registration, HMS Admin initiation, independent reviewer decisions, membership
activation or complete provisioning. Private evidence and pinned Compose JSON
are retained in the existing authorized QA fixture directory; no fixture secrets
or account payloads are committed.

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

The [API contract](../api/staff-identity-provisioning.md) records the new PUT/GET/POST
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
`1acc9d687cd161bf77689424fdbb3a1716decd9c`; SHA-256
`8e2f89da8e6f4771a7d0af3ca075094f2ec622ab6f0ce38475f9b7660018b382`.
CI downloads that exact source and checks the hash; there is no copied schema
master here. SQL was applied repeatedly only to an isolated PostgreSQL 16
fixture. The application cannot UPDATE/DELETE/TRUNCATE its four ledger tables;
tenant/product deletion cannot erase their history. There is no destructive
down migration: retain the facts on application rollback.

Chart 0.3.59 separately adds exact-command audit indexing and an ephemeral
hash-only history cursor cache. Current command/source authorization is always
required; opaque tokens do not contain or expose global audit position. Cache
expiry/deletion cannot erase identity or audit facts. Application-role row locks
work through a narrow hash-column privilege plus forced policies rejecting every
actual update and active deletion. Cleanup is bounded to 1,000 expired rows per
minute when Staff operations are enabled, with locked rows skipped.

### Private command activity continuation — 2026-10-31

History GET now returns bounded action/outcome/UTC summaries under exact current
machine/tenant/command/initiator scope, with no raw details, contacts, nonces,
global sequence or audit hashes. A 15-minute opaque continuation survives a new
service instance, preserves original expiry and excludes newer appends until
refresh. Tests cover unknown/tampered/foreign/expired cursors, missing/duplicate/
unknown query fields, wrong actor/product/tenant, withdrawal and source outage.
Mandatory-audit failure returns no page and rolls back any new token.

The route absence, non-UTC output and PostgreSQL application-role lock failure
were observed before correction. The new wire response uses the existing
`tenant_id` spelling. Real role tests prove continuation/cleanup, no active
deletion, no actual update and allowed expired deletion. A 1,105-row fixture
proves the 1,000-row bound and skip-locked behavior. All four immutable identity
ledgers remain outside cursor collection.

Final full owner race/coverage passed (54.3% overall, API 63.8%, store 61.6%), as
did vet/build/lint (zero issues), redacted secret scan, four delivery tests,
shell/diff checks and Linux/arm64 `helivanta-staff-onboarding-history:local` build.
Canonical SQL/hash were fetched and verified from the published schema commit;
its repository/PR/Helm gates passed. At 100,001 synthetic audit rows, the actual
bounded query used the new partial index with 0.046 ms local execution, inside
a rolled-back transaction. This is query-plan evidence, not an API SLO.
Exact owner candidate CI is recorded in the draft PR/checkpoint after commit.
No normal-role UI, operator recovery/replacement or production acceptance follows
from these history tests.

## Verification

The creation increment first reproduced two failures: different commands could
prepare the same case-insensitive contact, and creation PUT returned 404. Both
now pass. Real database/router tests cover new command reservation and creation,
exact/conflicting replay, eight concurrent requests with one provider create,
uncertain timeout/invalid results retained as 202, read-only lost-response
reconciliation, midflight entitlement withdrawal, replay-key withdrawal, closed
UTF-8/required-field validation and minimized responses. Contact races retain
one root/command, audit failure rolls both back, and historical commands cannot
manufacture roots. New schema Helm/repository gates passed. The full local owner
race/coverage suite passed (API 62.8%, store 58.7%), as did vet/build/lint (zero
issues). Overall coverage is 54.1%; this is not an overall completion percentage.
The final Linux/arm64 `helivanta-staff-onboarding-create:local` image build,
redacted secret scan, shell validation and four Python delivery tests passed.
At the local validation checkpoint, exact-head CI was not yet established.
The runtime creation increment was then committed/pushed as `f7bc168`; its
CI is [41076590905](https://github.com/tesserix/onboarding-service/actions/runs/41076590905).

An explicitly selected actual-provider test also passed against the separate
loopback v4.15.3 fixture. Owner HTTP creation durably recorded a new role-free
human, exact PUT replay retained its receipt, fresh observation left email
unverified, and actual membership/user-grant searches both returned empty.
Account evidence is stored privately as 0600 JSON with no password/verification
override. This test uses fixture-supplied registry proof and is not normal-role
UI/recipient acceptance. It fails rather than skips if its explicit loopback
ledger, private fixture or owner-readable credential is unavailable:

```sh
ONBOARDING_TEST_DSN='postgres://postgres@127.0.0.1:32768/postgres?sslmode=disable' \
STAFF_PROVIDER_FIXTURE_PATH=/absolute/private/local-provider-fixture \
mise exec go@1.27.3 -- go test -race -tags staff_provider_fixture \
  ./internal/api -run TestStaffIdentityCreationUsesActualLocalProvider -count=1
mise exec -- golangci-lint run --build-tags staff_provider_fixture ./...
```

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

- Real provider/consumer acceptance of creation PUT and reservations, including
  current human decisions and accountable handling of unresolved outcomes.
- Final review/CI and consumer acceptance of the event-store-backed provider
  freshness correction, including the configured provider version/capability
  and current organisation/registry proof; projected rows alone are insufficient.
- Current human authority in Helivanta before and after owner requests, its
  canonical client/Global API contracts and exact account/tenant UI binding.
- Consumer acceptance of private command activity and attributable operator recovery,
  including replacement/conflict/orphan handling. A missing provider user is
  never a reason to send the same create again.
- Complete recipient authenticator setup/sign-in and normal HMS initiation/
  reviewer integration. Actual local provider delivery/contact UI passed above;
  production-source delivery and full account-setup acceptance remain open.
  Synthetic `isVerified` fixtures are not proof.
- Reviewed existing-identity resolution and historical Staff/link/grant
  classification, followed by least-privilege Helivanta membership and independent
  current-person linking/review; no creation receipt activates operational access.
- Full owner/consumer failure, restart, race, isolation and UI acceptance, then
  exact approved SQL/source rollout and deployed acceptance. Do not close
  Helivanta A01/A02/A11 or tag a release from protocol/store tests alone.
