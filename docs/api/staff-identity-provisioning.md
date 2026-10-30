# Staff identity provisioning — draft owner contract

Status: PUT command creation, GET receipt and POST observation candidates are
implemented and locally tested, together with bounded private command activity.
Operator recovery/replacement and consumer UI remain in development.
Not accepted or deployed. Implements only
the identity-owner part of Helivanta A01; membership, Staff/person review,
employment and scoped operational access remain separate owner decisions.
See ADR-0004 and the Staff delivery checkpoint for remaining recovery/UI gates.

## Authorization and privacy

Only the current registered product machine matching the owner's explicit Staff
identity product configuration can use these operations, for its currently
entitled canonical tenant UUID and an active source organisation. No product key
is inferred by default: `hms` is a fixture key, and production registration is
tracked under owner issue #12.
`STAFF_IDENTITY_PRODUCT_KEY` enables the explicit registered key at startup;
empty disables Staff identity operations, and malformed values fail startup.
No human console, arbitrary product, browser cookie or platform role substitutes
for that machine boundary. The product is accountable for the human actor it
supplies: Helivanta must authorize its current Admin before the request, bind the
command to that account/workspace, and recheck before accepting/disclosing the
result. The owner does not infer a human authorization decision from a field.

Commands bind product machine, tenant and initiating human. Exact reads/retries
must retain that actor; another actor needs the separately authorized recovery
workflow. Source organisation/project identifiers come from fresh canonical
owner verification, never from request fields. No lookup by email, foreign
account enumeration, credentials or private contact response is provided.
All responses are `Cache-Control: no-store`; the expiring generic HTTP replay
cache cannot bypass current authorization on these routes.

## Create or replay

`PUT /v1/orgs/{tenant}/staff-identities/{uuidv7-command}`

The complete request, at most 4 KiB, has exactly these string fields:

```json
{
  "actor_subject": "opaque-initiating-human-id",
  "email": "synthetic@example.invalid",
  "given_name": "Synthetic",
  "family_name": "Staff",
  "reason": "Authorized Staff account creation"
}
```

IDs are bounded opaque source identifiers. Names must be nonblank, bounded and
free of control characters; email is one address, not a display-name expression.
Reason is required and bounded to 2,000 characters. Unknown/duplicate fields,
nulls, whitespace-only values, query overrides and non-UUIDv7 commands are
rejected. Passwords, verified-contact flags, roles, provider IDs, callback URLs,
source organisation and caller-selected correlation metadata are not inputs.

The owner persists immutable command facts and a case-insensitive contact root
within the source organisation, atomically with mandatory audit. Original email
is retained; the key uses the pinned provider's Go lowercase normalization,
without dot/plus rewriting. A different command cannot bypass a retained root.
Historical commands without roots cannot silently reserve or dispatch and need
reviewed classification. Provider policy can impose additional login uniqueness.
Roots are permanent; operator replacement must preserve explicit lineage.

The owner then commits one dispatch claim with audit. Only its winning caller may send one role-free human
creation. A concurrent/exact retry retains the original generated account ID and
nonce. Changed input or actor conflicts. A possibly dispatched create is never
automatically repeated, including after timeout, source absence or restart.

Return `200` when a creation receipt is durably recorded, or `202` for unresolved
dispatch, with a minimized command receipt: schema version, command ID,
tenant, product, initiating actor, `command_state` (`prepared`, `dispatched`,
`created`), immutable preparation time and, only when durably recorded, the
provider account ID and creation time. `dispatched` is an unresolved outcome;
it does not prove delivery, absence or failure. `created` proves retained
creation, not current identity state, contact verification, account setup or any
membership/employment permission. No source profile, contact, nonce or code is
returned. Do not permit a different new command to hide an unresolved one.

Source/registry scope is rechecked before dispatch and after provider I/O, then
again for the authorized/audited receipt read. The 25-second total budget includes
the provider's 15-second creation budget. Cancellation, withdrawal or audit failure
cannot authorize redispatch. Unavailable/invalid source results retain a dispatched
command rather than fabricated success. Observation may later retain only an
exact matching creation. This request never verifies contact, sets a password,
grants a role or activates membership.

## Protected state and fresh observation

`GET /v1/orgs/{tenant}/staff-identities/{command}?actor_subject={initiator}`
reads the exact retained command receipt under current owner authorization and
records disclosure audit. It performs no provider creation. The actor query is a
scope precondition from the accountable product, not a human-authentication
mechanism. Additional/repeated query fields are invalid.

`POST /v1/orgs/{tenant}/staff-identities/{command}/observations` accepts only
`actor_subject`. It reads current user and exact correlation metadata from the
provider, verifies the frozen account/org/contact/nonce/creation provenance and
rechecks current registry scope before recording/releasing an observation.
An exact final user read must preserve the first read's version/state. Missing,
inactive, locked, initial, unverified, malformed or unavailable evidence never
becomes current membership eligibility. Do not expose private source bodies.

Both user reads and the exact metadata lookup use v4.15.3 paths whose handlers
request projection triggering/awaiting (`true`), but can proceed to stale SQL
after a trigger failure. Separate actual-provider fault tests reproduced both
active state after committed deactivation and an unchanged nonce after a
committed replacement. Stable matching versions alone are insufficient.

After the final user read, the owner requests
`POST /admin/v1/events/_search` under its existing `events.read` capability.
Three exact descending limit-one queries establish the latest state mutation,
latest email change and latest email verification independently of the user row
revision. A skipped mutation followed by an unrelated profile update can advance
that row without repairing state/contact. Compare authoritative state, latest
contact and verification ordering directly; a higher row revision cannot replace
those checks. No state mutation means the v4.15.3 new-human active baseline;
the command binds its original email, and no later verification means unverified.
The exact account, organisation and user aggregate are filtered, together with
the 16 v4.15.3 state/initialization/email/metadata mutation types. Ascending
events after the lesser observed revision are bounded to 32 events and 64 KiB;
the pinned implementation awaits open event-store transactions. Unreflected
relevant mutations, foreign/malformed evidence, saturation, denied/unavailable
event reads and redirects fail unavailable. Metadata keys distinguish unrelated
updates from binding changes. Initialization state changes need explicit state
comparison because their projection does not advance the user's row revision.
Password/login events are excluded, and raw event payloads/editor data never
leave the owner. Missing capability must not be bypassed or silently expanded.
This detects committed relevant mutations at those read boundaries; it does not
lock future changes or establish current organisation/registry authority.

These reads are not a distributed transaction. Reject a creation time
older than the dispatch claim beyond a one-minute provider-clock allowance, or
one more than a minute in the future; a retained creation time must match
exactly. Observation timestamps must fall inside the actual observation call.
Source revisions remain lossless canonical positive decimal strings.

The POST is deliberately excluded from the generic expiring HTTP replay cache,
even if an Idempotency-Key is supplied. Repeated observations reread current
authority and source state. The creation receipt remains immutable; it is not
an eligibility cache. Prepared-only commands cannot be observed or dispatched
by this endpoint. A matching proof may reconcile a previously dispatched
command's creation receipt, including a currently inactive identity, without
claiming that identity is active or verified for membership.

A successful observation reports its timestamp, minimized state and lossless
source revisions. Contact verification is distinct from having a usable
authentication method; actual recipient login/account setup still needs UI
acceptance. The observation is a bounded point-in-time fact, not a distributed
lock or a promise of future authority. Consumers reauthorize at their own
command boundaries. A lost-response reconciliation may retain a matching
creation receipt but cannot issue another create.

Successful private state/disclosure reads require their audit in the same
database transaction. Observations include minimized state/verification and
source revisions in the disclosure audit. Existing cancellation-independent
best-effort audit attempts now have a separate two-second budget, so a blocked
denial audit cannot hold a cancelled request indefinitely. A failed mandatory
Staff disclosure audit releases no private receipt.

## Reviewed recovery annotations and eligibility

The protected receipt adds optional `recovery_state` and `successor_command_id`.
`closed_unresolved` retains an independently closed investigation without claiming
that the original source creation was unsent. `superseded` includes only the exact
reviewed successor command UUID. `command_state`, `user_id` and `created_at` remain
historical creation facts; these annotations are current recovery state. No raw
operator identity, reason/evidence, successor provider ID or binding nonce is
returned to the product. A bare approved replacement without complete lineage is
unavailable, not a fabricated ready successor.

PUT creation and POST observation return 409 for approved closed/superseded
commands. Observation checks before source I/O and again at final disclosure.
Closure during source I/O retains a valid late creation receipt for reconciliation
but denies creation/observation authority. GET receipt remains an attributable
historical read under its existing exact-current scope and mandatory audit.
Creation receipts alone never authorize Staff/login linking or membership.

Atomic reviewed replacement commits decision, fresh prepared command, permanent
root/parent lineage and audit together. It does not dispatch a provider account.
Only the exact successor can subsequently use normal creation; exact retries do
not redispatch it. The original dispatch and contact root stay retained. Products
cannot bypass uncertainty with arbitrary new command IDs. Explicit replacement
requires the platform operator workflow in ADR-0005; its authenticated endpoints,
source conflict/orphan checks and UI are still pending. This is an additive draft
v1 annotation, not approval to activate a new consumer or production policy.

## Bounded private command activity

`GET /v1/orgs/{tenant}/staff-identities/{command}/history?actor_subject={initiator}`
accepts optional `limit` (canonical integer 1–100, default 20) and `cursor` from
the prior response. Reject repeated/unknown/empty query values and any body.
The response binds schema version, command ID, `tenant_id`, product and initiating
actor, and returns `items` of action/outcome/UTC time, `has_more` and, when needed,
an opaque `next_cursor` with `cursor_expires_at`. Raw audit detail, contact/name,
nonce, provider credentials and global audit sequence/hash are excluded.

The current product machine, source/registry entitlement and exact original
command/actor binding are checked on every page. The actor field is a command
precondition, not proof of the current human; the product must authorize that
human before requesting/disclosing activity. Newer appends are outside an existing
descending page chain; refresh opens a new view. Serialized audit sequence is
used internally, through an exact-command partial index, without exposing it.

Continuation tokens are 32 random bytes with only a SHA-256 hash, scope binding
and internal position stored in an owner cache. They survive process/replica
changes but confer no authority. Foreign, tampered, unknown or expired cursors
return the same 400. Continuation keeps its original 15-minute expiry. Current
source outage returns 503; removed scope remains denied even with a valid cursor.
No data or new token is released if mandatory disclosure audit fails: audit,
page publication and new cursor storage share a database transaction.

The cache is separate from the four immutable identity ledgers. Forced database
policies permit expired deletion and reject active deletion/actual updates.
A narrow hash-column UPDATE privilege supports PostgreSQL row locks; an update
policy with false new-row check prevents mutation. With Staff operations enabled,
the API collects at most 1,000 expired rows per minute, skips locked rows and
uses a five-second maintenance budget. It never deletes identity/audit facts.
History uses a five-second service context; denied-audit fallback retains its
existing separate two-second budget. This activity endpoint is not
operator recovery, a replacement decision or an operational permission grant.

## Errors and recovery

Use the owner's existing problem document: 400 invalid input, 401 missing/invalid
machine authentication, 404 unknown or unauthorized tenant/command, 409 changed
command facts and 503 unavailable/ambiguous proof. After command dispatch becomes
possible, errors must not claim that no change occurred. Clients freeze the
original command and recover its exact receipt; they must not generate a new ID
because an observation request timed out.

Persist commands, claims and receipts through membership removal, account
deletion and application rollback. Source 404 after uncertainty cannot tell
never-sent from created-then-deleted. Explicit accountable operator resolution,
replacement/conflict/orphan handling and operator-scoped history remain
required before this contract can be accepted as full provisioning. No
credential rotation, live schema change or rollout follows from this draft.
