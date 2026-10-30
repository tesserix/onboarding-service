# ADR-0005: attributable Staff identity recovery

Status: implementation in progress, not a released recovery API. Extends
ADR-0004; source creation, contact verification and operational membership remain
separate decisions. This protocol is needed by Helivanta A01/A02/A11; no current
clinical, financial or employment authority is conferred.

## Reconciliation and approval admission — local candidate, 2026-10-31

The API contract now includes fresh write-authorized creation reconciliation and
independent immutable decisions. See the [owner contract](../api/staff-identity-provisioning.md#creation-reconciliation-and-independent-decisions--unreleased-candidates)
for dispositions, exact retries and disclosure rules. Matching accounts retain
original facts and prevent replacement; removed accounts retain historical birth.
Approval uses fresh source proof, not a prior observation response.

Receipt admission and new approval share a PostgreSQL transaction advisory lock
keyed by the original command. Append-only business ledgers retain SELECT/INSERT
privileges; row locks would require expanding them to UPDATE. Lock order is current
registry scope, command advisory lock, audit. No provider I/O occurs inside the
transaction. The new-decision precondition requires exact retained birth or absence;
frozen retries/rejections do not rerun it. Twelve concurrent PostgreSQL receipt/
approval races verify either approval precedes the late receipt or approval rolls
back. Late facts after an already-approved decision remain historical.

No new schema or migration is required by this step. Existing receipt and decision
constraints remain canonical in the separately pinned schema-owner candidate.
Normal-role UI, real-source full HTTP and operational latency remain release gates.

## Current operator source proof — local candidate, 2026-10-31

The provider adapter now verifies an active, verified human in its configured
platform organization and one exact direct grant for the configured console
project. Read needs the current `platform` role; writes additionally need
`rotate-credentials`. Grant discovery is exact user/project, limited to two rows;
missing authority denies, ambiguous/invalid proof is unavailable. No grant,
membership, password or contact verification is changed by this adapter.

Grant projections and token-time roles alone are insufficient. The v4.15.3
authoritative org/project/usergrant events establish current state and roles;
project role definitions are checked independently because cascade failure may
leave removed roles in a grant projection. Source object heads are captured
before bounded fact reads at one sequence upper bound and must remain identical
afterwards. The user projection is reread exactly and compared with authoritative
state/contact/verification events. Wrong object/owner, source churn, malformed
JSON, redirects, unavailable source and saturated history release no proof.

Each source proof has a four-second budget and at most 64 returned historical
events per object. Saturation is an explicit availability failure, never an
authorization fallback. Long grant/project histories require a separately
reviewed bounded-history continuation before deployment; no pruning or automatic
role recreation is authorized. Actual p99 latency remains unmeasured.

The principal preserves verified console access-token origin. Recovery-specific
policy actions reject legacy cookies, product machines, customer admins and
ordinary users; platform operators can only read. This policy is an initial
check, not a replacement for the live source proof or target-scope/store checks.
The source adapter and policy now guard the unreleased operator receipt read,
exact request-detail read and proposal endpoints. They also guard the independent decision and creation-reconciliation candidates;
full real-source HTTP and complete reviewer UI remain required.

## Audited operator read/proposal service boundary — local candidate

The fresh source-view POST now consumes the exact-account classifier under the
verified console-reader boundary. Frozen input is read privately from the exact
dispatched command; current operator/tenant/product/source scope is rechecked
after subject I/O and before mandatory transactional disclosure audit. The
response exposes only disposition and observation/birth time. It creates no
account, receipt, decision, closure or successor. Generic replay cache exclusion
is regression-tested: fresh source changes are visible and later revocation
cannot recover an old success. Independent approval must call the source verifier
again and cannot treat an earlier observation response as authority.

Classification is `absent` only after two actual missing reads and stable empty
object history. `matching` requires retained birth/contact and current sealed
binding through the existing observer. `removed` additionally requires actual
missing reads, terminal removal and bounded history proving one human birth and
the latest causal nonce. Source errors, stale projection, contradiction, unknown
history or saturation fail unavailable. Four-second proof and 32/64-event bounds
remain explicit availability limits. No universal contact-availability claim is
made. Actual isolated v4.15.3 and projection-fault tests verify the classifications
and refusal of a stale 404, with captured projection state restored afterwards.

Exact request detail now uses the same current-human boundary with two live
source/operator proofs and a final audited store read. Its request/decision/
successor projection uses one statement snapshot, exposes only the evidence
needed for review and omits original contact/profile/source bindings. A committed
decision that appears during reverification is included in the final snapshot;
authority or scope withdrawal returns no private evidence. Store lineage
inconsistency, including bare approved replacement, fails closed. Request read
does not assert approval authority or current eligibility.

The [pinned-provider source-proof research](../research/staff-identity-recovery-source-proof.md)
records the next approval gate. User search reads projection rows and its
checkpoint separately; an empty result/checkpoint is not authoritative absence.
Exact frozen object history, matching orphan reconciliation and conflict handling
must be implemented/tested before independent approval is exposed. A prepared
replacement must never be described as contact availability or operational access.

The service retains the real `KindPlatform` principal and verified console-token
origin; it never invokes product-only identity verification under a fabricated
machine identity. It independently verifies the exact canonical tenant/source
organization, active configured product/source project and current entitlement.
Store scope follows the current canonical machine while command facts remain
bound to the original initiator, machine and provider user.

Receipt reads perform live authority/source verification, mandatory audited
inspection, a fresh authority/source proof and a final transactional audited read.
Proposals derive the maker from that principal, require immutable reason/evidence,
recheck authority/source before the proposal transaction and again afterwards,
and perform a final audited transactional scope check before returning a minimized
recording receipt. Only a previously dispatched command can be proposed. No
provider write or recovery decision occurs. The shared operation reader preserves
original-actor behavior and closed/superseded receipt annotations.

Proposal request IDs own durable retry semantics. A regression reproduced the
generic replay cache returning a success after maker revocation; explicit route
exclusion now requires fresh authority on every retry and retains no generic
cached response. If authority is withdrawn after proposal commit, no recording
receipt is released, while the immutable proposal/audit remain. Restored authority
can recover the same proposal through an exact retry. Independent decision state
must not be inferred from this immutable recording receipt.

Each private store read locks/rechecks canonical scope, requires exact human
platform audit binding and rolls back on audit failure. The full service budget
is 25 seconds; read performs two source/operator proofs and proposal up to three.
Operational latency and normal-role UI are not yet accepted. Database/HTTP
integration tests use callback source proofs; real provider adapter proof is
separate evidence. No normal HMS Admin/recipient/reviewer UI is inferred.

TLS protocol tests cover withdrawals, foreign scope, stale projection, role
definition deletion, malformed/ambiguous proof and source churn/saturation.
An explicitly selected isolated actual v4.15.3 provider test verified grant
deactivation/reactivation, definition-removal write denial with read preservation,
and disabled-human denial. Its backend-created synthetic authority fixtures do
not establish normal human login or HMS Admin/recipient/reviewer UI acceptance.

## Capacity and boundaries

Design envelope, not production measurements: at most 2 recovery commands/second
per instance, each future API input bounded at 4 KiB, and 10 reads per write.
At 10,000 requests/year, separate 4 KiB request and decision inputs add at most
approximately 80 MiB/year before server-derived bindings, indexes and audits.
Database reasons are bounded at 2,000 Unicode characters per record; the future
HTTP byte bound remains a separate check. Three years fit one existing
PostgreSQL owner without sharding. In-process/database operations target
300 ms p99; actual service SLO and observed demand still need release review.
No new service, cache, broker, worker or ongoing saga is introduced. Each request
or decision is one short transaction with its mandatory audit; source reads and
provider creation remain outside transactions. Existing identity calls retain
their own 25-second budget.

## Authority and immutable facts

Only an authenticated platform administrator can submit/review owner recovery;
its subject comes from the verified principal, never an asserted request actor.
The product machine is not an operator. A read-only platform operator cannot
write. The exact tenant/product/source/command and current entitlement are checked
at the boundary and again before committing any active replacement. A product
cannot use recovery to enumerate another tenant or impersonate another initiator.
HMS remains accountable for current human authority in its consumer journeys.

A request retains an opaque UUIDv7, original dispatched command, original
initiating actor, requesting operator, requested action, required bounded reason,
opaque evidence reference and optional replacement initiator. It does not retain
codes, passwords, raw source errors or an automatically fetched evidence URL.
Requests and decisions are append-only and restricted by FKs to exact immutable
facts. A reviewer must differ from the requester, original initiator and original
provider-user subject. The
replacement initiator is also excluded from approval. Rejection is final for its
request; a new request can retain corrected evidence without changing history.
Only one approved replacement decision may win for an original command, including
when competing requests are approved concurrently. Attributable closure of an
unresolved investigation does not prevent a later reviewed replacement; closure
continues to deny the old dispatch and does not reserve a successor.

## Dispositions and replacement lineage

`close_unresolved` is an attributable closure of investigation. It does not claim
creation failed or the identity never existed. `replace` requires an explicit
new command and permanent predecessor/root linkage. Approval, replacement
preparation and audit must commit together; a bare approved decision is not a
permission to dispatch or to insert arbitrary replacement input.

The permanent original contact root and original dispatch remain retained. New
provider ID/nonce belong to the successor only. Exact retries cannot resend the
old creation. Later discovery of an orphan does not silently link or activate
it; platform reconciliation retains its conflict/outcome. Existing active source
accounts need attributable conflict/orphan handling before replacement acceptance.
Source absence after uncertain dispatch never proves that creation was unsent.
Parent commands that have been closed/superseded cannot become membership
eligibility through receipt/observation replay. The product must display the
retained recovery status and independently authorize actual linking/membership.

## Failure, schema and rollback

Unavailable identity, source verification, canonical registry or mandatory audit
fails closed. No proposed/rejected request authorizes anything. Competing approvals
are serialized by database uniqueness; transaction rollback leaves no partial
accepted decision, successor or audit. Exact retries recover the frozen result;
changed input conflicts. Do not delete history to retry an operation.

The schema owner is tesserix-k8s onboarding_db.sql, additive only; application
CI must pin its immutable commit/hash. Tests exercise actual PostgreSQL constraints,
application-role write denial, concurrent decisions and audit rollback. Durable request/decision constraints and atomic successor lineage/store execution
are implemented. Verified operator/source authority, owner endpoints and
consumer/operator UI remain required. None of those later
gates is implied by the existence of these tables. Existing binaries ignore new
tables; rollback retains their facts and never drops them. Deployment/migration
requires named approval and full compatibility/normal-role acceptance.

Single-table mutable status was rejected because correcting or replacing it would
lose the accountable request/decision history. Automatic redispatch was rejected
because a timeout/404 cannot prove whether the original account was created.

## Implemented transaction and disclosure behavior

An immutable lineage row binds the exact approved request, parent, successor and
original contact root under one tenant/product/source project/contact. Unique
parent/successor/request and an exact predecessor-generation self-FK prevent
branching/cycles without trusting UUID timestamp ordering. Indexed root/parent
point queries avoid recursive scans. Decision, successor and lineage roll back
with mandatory audit failure; no provider dispatch occurs in the transaction.
Actual application-role tests cover decision/retry plus UPDATE/DELETE/TRUNCATE
denial. Exact retry retains reviewer/reason and successor ID; server-generated
provider ID/nonce remain frozen despite regenerated retry candidates.

Receipt annotations preserve immutable creation facts while marking independent
closure or supersession. Creation and observation recheck current recovery state
before final disclosure, deny closed/superseded authority and retain late source
creation facts for reconciliation. A bare approved row cannot manufacture a
successor. This is not the verified operator authorization/source quarantine
protocol or the full HMS Admin/recipient/reviewer acceptance. Those gates stay
open before exposing operator writes. Platform recovery events also need a
minimized authorized activity projection for their operator/product consumers.

Concurrent indexes require autocommit canonical SQL execution. Release must
inspect actual indexes/FKs/privileges; bootstrap ON_ERROR_STOP=0 means a green Job
is not migration proof. Prepared commands stranded by canonical machine-account
replacement, legitimate profile correction, and late source-orphan reconciliation
need explicit operator acceptance; do not infer those cases from dispatched-parent
fixtures or a store-only approval.

Historical independence extends beyond the immediate parent: original-root and
all retained successor creators/provider-user subjects are barred from reviewing
later-generation recovery. Two root/subject indexes support bounded equality
probes. Second-/third-generation PostgreSQL tests reproduced the prior bypass and
now deny root/intermediate parties. These application-owner guards supplement,
rather than replace, the database's immediate-request exclusions and the future
verified-current-operator authorization boundary.
