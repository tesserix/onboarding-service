# Staff identity provisioning implementation checkpoint

Updated: 2026-10-31. Status: draft, incomplete; do not merge or deploy as completed
provisioning. Owner [PR #22](https://github.com/tesserix/onboarding-service/pull/22)
and canonical SQL [PR #1616](https://github.com/tesserix/tesserix-k8s/pull/1616)
are coordinated with Helivanta A01–A12. Exact protected receipt and observation
routes, creation PUT and bounded command activity are implemented in the current candidate. Human
authorization in Helivanta, recipient verification, membership and employment
activation are not implemented by these endpoints.

## Independent decisions and orphan reconciliation — local candidate, 2026-10-31

Implemented fresh write-authorized reconciliation and independent decision HTTP
candidates. A maker cannot review; exact retries recover immutable history without
repeating subject preconditions. Matching source accounts retain original creation
facts and block replacement. Removed accounts retain confirmed historical birth;
absence approval atomically requires no intervening receipt. Server-generated
provider IDs/nonces belong only to an atomically prepared successor; approval does
not dispatch it or grant membership. Entire recovery route family bypasses replay
cache, including on revoked-operator retries.

PostgreSQL/HTTP targeted race tests pass for closure, rejection, approved replacement
from absent/removed proof, matching-account conflict/reconciliation, unavailable
source, invalid/asserted input, foreign tenant and revoked retry. New store tests
also verify exact birth/absence preconditions and twelve concurrent receipt versus
approval races. These HTTP tests use callback source proofs; normal Admin/recipient/
reviewer UI and full real-source owner HTTP are still unproven. Final validation for
this candidate passed: full PostgreSQL Go race/coverage suite (55.0% overall,
API 67.0%, store 67.9%, provider 87.8%); vet/build; default and provider-fixture
lint (zero issues); four Python delivery tests; redacted Gitleaks and diff checks.
Separate cross-package API tests exercised decision service at 83.6%, final
decision disclosure at 83.3% and reconciliation service at 70.3%. These are test
coverage figures, not completion percentages. Authority-withdrawal tests verify
no decision before commit, retained history without disclosure after commit, and
authorized exact recovery without repeating source approval checks. No new migration, merge or
production rollout occurred. Remaining full Access/RBAC scope is unchanged.

The checkpoints below retain evidence from earlier candidates; their remaining-work
lists describe those checkpoints. The independent-decision section above is current.

Commands run from `apps/api/` for this candidate:

```sh
ONBOARDING_TEST_DSN='postgres://postgres@127.0.0.1:32768/staff_recovery_20261031?sslmode=disable' \
  mise exec go@1.27.3 -- go test -race -p 1 -covermode=atomic \
  -coverprofile=/tmp/staff-recovery-decisions-20261031.cover ./...
mise exec go@1.27.3 -- go vet ./...
mise exec go@1.27.3 -- go build ./...
mise exec -- golangci-lint run ./...
mise exec -- golangci-lint run --build-tags staff_provider_fixture ./internal/zitadel
```

From the owner root: `python3 -m unittest discover -s tests -v`,
`gitleaks dir --redact --no-banner .` and `git diff --check` passed.
No provider implementation changed in this candidate; actual-provider evidence
below belongs to the previous classifier candidate, not a new end-to-end UI run.

## Exact-account classification and fresh reviewer source view — earlier candidate

The new source verifier classifies only the frozen account: absent after two
actual missing reads and stable empty authoritative history; matching after
retained birth/contact and current sealed nonce proof; removed after actual
missing reads, terminal removal and bounded history proving one human birth plus
the latest causal nonce. It never changes the source. TLS race tests cover stale
404, contradictory ownership/contact/nonce/birth, source churn, lost historical
binding, multiple births, saturation, dependency errors, invalid input, ambient
cookies and redirects. There is no contact-availability or operational-access claim.

The current-human reviewer source-view POST uses immutable stored input, validates
proof identity/owner/freshness and bounds, rechecks live authority and canonical
scope, and requires final transactional audit. It exposes only disposition and
observation/birth time. It creates no source account, retained creation receipt,
decision or successor. A cache regression first hid changed facts; explicit route
exclusion now forces fresh observations and denies revoked authority. Database/
HTTP integration tests use callback subject/operator proofs, not normal UI.

Actual isolated v4.15.3 race fixtures passed absent/matching/removed account proof
and the previous operator withdrawal checks. Asynchronous email delivery changed
the account head during immediate matching proof; a later unchanged read passed.
The fixture performs at most three fresh read attempts and creates once; final
matching took two proof attempts. Recreation details are captured privately before deleting
only the newly created role-free synthetic account.

A separate actual projection fault captured one retained synthetic account's
parent/child rows with mode 0600, temporarily removed its projection, observed real
GetUser 404 and verified that history prevented absence proof. Restore initially
failed on a generated notification column; it was corrected, all captured rows
were restored, and successful race reruns verified exact captured rows and matching
source proof. This modifies only the named isolated fixture, never event history
or production. Backups stay in the existing private fixture directory.

Full actual-PostgreSQL owner race/coverage passed: 56.0% overall, API 66.0%, store
66.7%, provider 87.8%; subject classifier 93.6%, removed ownership 95.8%. Separate
cross-package source-view service coverage is 85.7%. Vet/build, default and
fixture-tag lint (zero issues) passed. Final delivery/secret/diff and exact-head
CI evidence are tracked separately. Coverage is not feature completion.

The API docs now match actual `CONSOLE_PROJECT_ID`/paired `CONSOLE_CLIENT_ID`
configuration and the existing two-second provider creation budget; no runtime
timeout was changed. No schema/migration/production rollout, merge or release
occurred. Remaining: fresh independent decision service/API, matching-orphan
receipt reconciliation, explicit conflict handling, recovery activity, prepared-
command/profile correction, consumer UI, recipient setup/sign-in, canonical
registration (#12), legacy grants and all remaining A01–A12 lifecycle/cross-module
cutoff acceptance. Normal Admin/recipient/reviewer UI and real-source full owner
HTTP acceptance remain unproven; full Access/RBAC stays open.

Actual provider/fault commands are explicitly selected with `staff_provider_fixture`:
`TestStaffRecoveryOperatorActualProvider` requires the existing private fixture
directory. `TestStaffRecoverySubjectProjectionFaultActualProvider` additionally
requires an existing active subject capture basename in
`STAFF_RECOVERY_SUBJECT_CAPTURE`, the named isolated DB container in
`STAFF_PROVIDER_FAULT_CONTAINER`, and explicit local Unix `DOCKER_HOST`. It fails
before mutation if those are unavailable and retains private restoration captures.

## Exact recovery request review — local candidate, 2026-10-31

The new current-human GET by exact tenant/command/request returns immutable maker,
requested action, reason/evidence reference, optional proposed successor initiator
and any retained independent decision/successor command ID. It omits original
provider account/owner/project, nonce, contact/profile and machine bindings. The
same verified-console reader boundary, two live operator/source proofs and final
transactional target check apply. Request/decision/lineage use one statement
snapshot; an independent decision committed during reverification is reflected
in the final read. No approval authority or eligibility is inferred.

Mandatory disclosure audit targets the exact request. Tests pass for undecided,
rejected and complete approved-replacement snapshots; a bare approved replacement
without lineage fails closed. Foreign scope, missing requests, withdrawn
entitlement, malformed IDs/body/query, source outage, operator revocation,
canonical rotation and audit failure release no private request evidence. The
proposal/decision audit-outcome regression first committed business actions with
`denied` audit outcomes; both store commands now require matching success audit
and the regression verifies no retained command on rejection.

The [source-proof research](../research/staff-identity-recovery-source-proof.md)
was verified against pinned v4.15.3 via `git show`, not the unrelated modified
source checkout. User search reads rows and projection checkpoint separately;
neither empty search nor checkpoint proves authoritative absence. Replacement
preparation must not be confused with contact availability or operational access.
Next source work must classify the exact frozen account, reconcile matching
orphans and fail closed on contradictory/missing/ambiguous source proof.

The final full actual-PostgreSQL race suite, vet, build and lint (zero issues)
passed after the audit-outcome fix: API 65.6%, store 66.7%, provider 86.7%.
Cross-package integration
measured actual request-review service execution at 89.7%; audited request store
read 90.0% and HTTP handler 94.1% in the full suite. These are coverage figures,
not completion. Four delivery tests and redacted secret/diff checks passed.
New HTTP/database tests use callback source proofs; normal HMS Admin/recipient/
independent-reviewer UI and a real-source full HTTP journey remain unproven.

Parent 74abd3d exact CI 41111788806 passed; that is historical for this candidate.
No schema/production change, migration, merge, release or completion tag occurred.
Remaining: source conflict/orphan verification, independent decision service/API,
request activity, prepared-command/profile correction, consumer UI, recipient
setup/sign-in, canonical registration (#12), legacy grants and all remaining
A01–A12 lifecycle/cross-module cutoff acceptance. Full Access/RBAC stays open.

## Recovery read/proposal service and HTTP boundary — local candidate, 2026-10-31

The source authority adapter now guards two unreleased operator routes: exact
command recovery receipt GET and immutable proposal PUT by UUIDv7 request ID.
The real platform-human principal is retained throughout; no product principal
is fabricated. Canonical active tenant/product/entitlement and exact source
organization/project binding are verified independently, while original command
actor/machine/provider facts remain frozen across canonical machine replacement.

Private inspection and final reads require an exact human-platform disclosure
audit in the same store transaction. The shared operation reader preserves the
original-actor receipt and closed/superseded annotations. Proposal derives maker
from verified identity, requires reason/evidence, and records only an immutable
request for an already-dispatched command. It creates no decision, successor,
user, membership, closure or dispatch. Source authority is rechecked around reads
and writes, with a final transactional target check before disclosure. The API
contract documents input/output, errors, configuration and the 25-second budget.

A regression reproduced a successful proposal replay after maker revocation from
the generic idempotency cache. Explicit exclusion fixed it; every retry now
reauthorizes against the live source and the durable ledger owns exact retries.
Tests also cover forged maker/reviewer/credentials, duplicate/case-variant keys,
invalid UTF-8, missing/invalid evidence, controls and wire bounds; roleless,
legacy/product/customer/user denial before source I/O; wrong/stale/future proof;
scope or entitlement changes during final source I/O; original fact retention;
mandatory audit binding/rollback; and proposal commit followed by authority
withdrawal with no receipt, then successful exact recovery after restoration.

Full actual-PostgreSQL owner race/coverage passed: 55.4% overall, API 64.8%, store
65.6%, provider 86.7%. Vet/build and lint (zero issues), four delivery tests,
redacted secret scan and diff checks passed. The separate cross-package race
integration run measures actual service execution: recovery receipt 94.1%, scope
79.5%, proposal 80.6%, input validation 100%; audited recovery store read 92.9%
in the full suite. Coverage is test evidence, not feature-completion percentage.

Source/registry callbacks drive these new database/HTTP integration tests. The
actual isolated v4.15.3 source-adapter test at parent 2e68596 is separate protocol
evidence. Normal HMS Admin/recipient/independent-reviewer UI and a real-source
full HTTP journey remain unproven. No schema or production change, migration,
merge, release or completion tag occurred. Remaining: minimized request detail/
activity, source conflict/orphan verification, independent decision service/API,
prepared-command/profile correction, consumer integration, recipient setup/sign-in,
canonical registration (#12), legacy grants and all remaining A01–A12 lifecycle/
cross-module cutoff acceptance. Full Access/RBAC remains open.

## Recovery-operator authority checkpoint at 2e68596 — 2026-10-31

The new source verifier checks an active/verified human, exact platform owner,
direct console-project grant, authoritative org/project/grant state and current
role definitions/keys. It compares exact relevant event heads before/after
bounded snapshot reads and rechecks the human projection against authoritative
state/contact events. No source role or membership is changed by verification.
Withdrawn/foreign authority denies; stale/malformed/ambiguous/changing/saturated
proof releases no result. See ADR-0005 for bounds and remaining availability work.

HTTP authentication now preserves verified console-token origin on platform
principals while registered machines remain product principals. The regression
first returned console=false for a verified console token, then passed after the
correction. Recovery policy distinguishes reader/proposer/reviewer actions,
denies legacy-origin administrators and product/customer/user credentials, and
requires the write role for proposal/decision. These policy actions and source
proof are not yet integrated into service endpoints.

TLS fixture cases cover current/read authority, withdrawn grants and
roles, removed definitions, foreign scope, stale user/contact proof, missing or
malformed history, source churn/saturation, redirect/cookie isolation and provider
failures. An explicit actual v4.15.3 local
provider test passed current proof, grant deactivation denial/reactivation,
role-definition removal write denial/read preservation and disabled-human denial.
Synthetic operator authority was created through the local provider backend;
this is not human-login or normal Admin/recipient/independent-reviewer UI proof.

Full owner actual-PostgreSQL race/coverage passed: 56.4% overall; API 63.8%,
authz 95.2%, store 64.9%, provider 86.7%, operator verifier 94.0%. Vet, build,
lint (zero issues), four delivery tests and the redacted secret scan passed. No schema or
production permission changed, and no merge/migration/rollout occurred. Remaining:
service pre/post authority checks and source/registry scope, conflict/orphan proof,
authenticated audited endpoints/activity, prepared-command/profile correction,
canonical registration, recipient authenticator setup and all remaining A01–A12
normal-role UI/lifecycle/cross-module acceptance. Full Access/RBAC remains open.

## Historical self-review guard — 2026-10-31

A review of later-generation recovery identified an independence bypass: the
original root creator/account, or an intermediate successor creator/account,
could approve after a new command ID replaced its immediate binding. Actual
PostgreSQL cases reproduced accepted second-/third-generation self-review before
the fix. Owner decision execution now reads immutable root history and excludes
all retained identity creators/provider-user subjects, in addition to the current
request's four interested subjects. Equality-probe indexes keep the checks bounded;
there is no recursive ancestor walk and no user-supplied root/party list.

Canonical schema is now `325012bc6fbaf199413dbce6ed58406be4ca66e5`, chart 0.3.62,
SHA-256 `aa89167dad24dcfb3fd71e305cad29f573d1604ffc99ee75f9e8f459b4880756`.
Its published immutable source was hash-verified and pinned for owner CI. Two
concurrent root/subject indexes support the application-owner historical guards;
the database's immediate-request FK/check exclusions remain unchanged. No new
operator endpoint or source permission was enabled.

The new historical case and previous transaction/API cases pass: fifteen new
checks in this increment plus fourteen earlier recovery tests. Full owner actual-
PostgreSQL race/coverage passed: 55.1% overall, API 63.8%, store 64.9%, decision
83.6%, successor/root helpers 83.3%, shared recovery scope 100%. Vet/build/lint
(zero issues), four delivery checks, redacted secrets and shell/diff checks pass;
schema Helm lint/template and seven selected repository tests pass. These are
backend/contract/race tests, not full normal-role UI or real-provider E2E evidence.
Current verified operator authority/source proof, endpoints/activity/UI, prepared-
command/profile correction, recipient setup, canonical product registration,
legacy classification and remaining A01–A12 acceptance remain open as below.

## Atomic decisions, lineage and stale-authority denial — 2026-10-31

Owner storage now commits an independent decision, prepared successor, permanent
root/parent lineage and disclosure audit atomically. There is no provider dispatch
inside approval. A rejected request remains final. Exact retry returns frozen
reviewer/reason/successor ID and original server-selected provider ID/nonce;
regenerated server candidates cannot change the retained identity. Competing
replacement transactions have one winner. Bare approved rows cannot materialize
an unrecorded successor. Actual application-role decision and exact retry passed;
lineage UPDATE/DELETE/TRUNCATE are denied.

Schema-owner `9dc4638b7a043c5509eb59d1893545a36b2d64ed`, chart 0.3.61, SHA-256
`cbed5780340d67424cb7779cd76479e5bb9786192170b668cc0ab5e8c35f1ee1`,
is published and hash-verified in the owner CI pin. Exact-scope composite FKs,
parent/request/successor uniqueness and a predecessor-generation self-FK preserve
one non-branching root lineage without recursive reads or UUID-clock assumptions.
Indexes on existing ledgers are concurrent/autocommit. The bootstrap runner uses
ON_ERROR_STOP=0: actual index/FK/privilege checks are a release gate, not Job status.

The normal product creation path now admits the exact reviewed successor and
blocks an approved closed/superseded parent. Protected receipts preserve historical
creation facts and add minimized recovery_state/successor_command_id annotations.
Creation and observation reject closed authority, including closure during source
I/O, while retaining late creation receipts for reconciliation. Parent receipts
expose no operator reason/identity, child provider ID or binding nonce.

Fourteen added checks passed (ten store transaction/lineage and four API scenarios),
in addition to the prior fourteen recovery checks. Tests cover exact retry,
independent/final rejection, ten competing approval transactions, atomic audit
rollback, bare approval, second-generation lineage, actual application privileges,
normal successor preparation/creation/replay and before/during-source closure.
API cases inject source callbacks; they are contract/race evidence, not normal
human UI or real-provider end-to-end acceptance. Full actual-PostgreSQL owner
race/coverage passed: 55.0% overall, API 63.8%, store 64.6%, decision 84.8%,
successor preparation 81.8%, shared recovery scope 100%. Vet/build/lint (zero issues),
four delivery checks, redacted secrets and shell/diff checks passed. Schema local
Helm lint/template and seven selected repository tests passed.

Remaining release work: verified operator authorization and source conflict/orphan
proof; authenticated operator endpoints and minimized recovery activity; prepared
commands stranded by machine-account replacement and legitimate profile correction;
normal operator/HMS Admin/recipient/independent reviewer UI; recipient authenticator
setup/sign-in; canonical production product registration (owner #12); legacy
staff/link/grant classification; remaining A01–A12 lifecycle/cross-module cutoff.
No operator write endpoint was exposed, no production migration/rollout occurred,
and no full provisioning, Access/RBAC or normal UI acceptance is claimed.

## Recovery record implementation — 2026-10-31

[ADR-0005](../adr/0005-staff-identity-recovery.md) defines recovery without erasing
uncertain creation or granting implicit membership. The new store operation
retains exact proposal facts and its audit atomically; it checks the current
canonical product machine/entitlement/source scope and retains the original
command/initiator/source-user binding across product-machine rotation. Exact
retries recover frozen facts; changed actor/action/reason/evidence conflicts.
A matching platform-operator audit is mandatory, including on replay. A failed
audit releases no private result and persists no new proposal. This is a store
operation, not yet an authenticated operator endpoint.

Canonical SQL is pinned to tesserix-k8s
`8fa241651362392d8c90322e97947066688bb832`, chart 0.3.60, SHA-256
`b9bfb2a34e7e8ff0708ce4bccaf52121e8a64557c7ff21a160189332e4a0b14c`;
the published immutable source was fetched and its hash verified. Two new
SELECT/INSERT-only ledgers retain exact dispatched-command requests and decisions.
Composite FKs plus checks reject review by maker, original initiator, original
source account and replacement initiator, including NULL/forged binding attempts.
A rejection is final for its request. One competing replacement approval wins;
closure of an unresolved investigation allows later reviewed replacement and
preserves the original dispatch/contact root.

Fourteen added actual-PostgreSQL tests passed, including twelve concurrent
replacement decisions, real application-role mutation denial, machine rotation,
withdrawal, scope drift, exact retries, audit binding, audit rollback and complete proposal/replay execution with actual
application-role privileges. The full
owner race/coverage suite passed (54.5% overall, store 62.7%, proposal operation
87.0%), plus vet/build/lint (zero issues), four delivery checks, redacted secret
scan and shell/diff checks. Schema-owner local tests passed (seven selected
repository tests, Helm lint/template and canonical SQL application).

Atomic decision execution/successor preparation and permanent root lineage,
current verified operator authorization, source conflict/orphan handling,
operator/product endpoints and UI, normal HMS Admin/recipient/reviewer acceptance,
production product registration and full A01–A12 lifecycle/cutoff remain required.
Neither a proposal nor the schema's approved decision row alone grants dispatch,
source-verification, linking or membership authority. No production migration,
merge, rollout or release occurred.

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
