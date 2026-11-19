# Subject-wide Staff MFA recovery admission

Status: implementation decision for Helivanta #1703; unreleased.

## Context

Zitadel factors belong to one globally identified native account. Legacy HMS
memberships can reference that same subject in several tenants. A tenant-only
recovery fence would permit a pending account recovery to be bypassed through
another membership. IAM already revokes credentials globally per native subject.
Separate tenant-owned accounts remain distinct; a shared email is not a subject.

Use the recovery design's planning envelope: 10 peak recovery commands/second,
50/150 tenants, 99.9% availability, two-second status-read target. Protected
traffic already obtains an uncached identity-owner credential projection; recovery
adds one indexed latest-subject lookup there, no additional HTTP hop. These are
planning assumptions, not measured capacity. The bounded result is below 1 KiB.

## Decision

Retain tenant-owned immutable reset commands and source authorization. Serialize
open commands globally per exact native subject with a database uniqueness fence
and advisory transaction lock. Completion checks the latest non-cancelled subject
generation. A cancelled preparation restores the preceding retained requirement;
completed requirements remain current until superseded by another preparation.

Add an explicit opt-in to the existing private Helivanta credential-status read.
The workload receives only `none`, `blocked`, `enrollment_required` or `completed`,
plus the exact command UUID as a generation ID when a requirement exists. No
originating tenant, employer, administrator, reason or native evidence is exposed
by this projection. Old callers receive their unchanged shape. Only active exact
subjects in a current authorized tenant read receive the projection; status
continues to support legacy memberships and never grants membership or roles.

HMS must compare a server-admitted generation on every protected admission path
and renew/transition it without inventing new proof. Native session/enrollment
verification and latest-generation recheck remain required before admission;
`completed` alone does not authenticate the current session. Preparation,
processing and uncertainty deny; acknowledged removal requires fresh enrollment.
Missing dependencies, malformed evidence, audit failure and unknown schema fail
closed. No TTL or native write retry clears a fence.

## Consequences and release

An owner recovery blocks the same account's legacy memberships across tenants;
it neither transfers roles nor reveals another employer's source. Independent
accounts sharing an email are unaffected. The index is additive on the new,
unreleased table; disposable hosted PostgreSQL verifies real query behavior.
Apply the reviewed schema before opting consumers in. Live migration needs named
approval. Code rollback retains command history and denies incomplete recovery;
never delete rows or undo credential revocation as rollback. No standing service
or infrastructure cost is added beyond indexes in the existing database.

Rejected: tenant-only admission (legacy bypass), per-request extra HTTP (avoidable
dependency hop), or using email as the global key (merges unrelated accounts).
