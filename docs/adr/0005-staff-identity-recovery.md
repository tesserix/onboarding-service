# ADR-0005: attributable Staff identity recovery

Status: implementation in progress, not a released recovery API. Extends
ADR-0004; source creation, contact verification and operational membership remain
separate decisions. This protocol is needed by Helivanta A01/A02/A11; no current
clinical, financial or employment authority is conferred.

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
application-role write denial, concurrent decisions and audit rollback. Current
work starts with the durable request/decision constraints, then implements atomic
successor lineage, owner endpoints and consumer/operator UI. None of those later
gates is implied by the existence of these tables. Existing binaries ignore new
tables; rollback retains their facts and never drops them. Deployment/migration
requires named approval and full compatibility/normal-role acceptance.

Single-table mutable status was rejected because correcting or replacing it would
lose the accountable request/decision history. Automatic redispatch was rejected
because a timeout/404 cannot prove whether the original account was created.
