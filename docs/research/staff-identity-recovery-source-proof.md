# Recovery source proof — 2026-10-31 research checkpoint

This records primary-source findings and the next acceptance gates. It is not an
activated approval policy or evidence of a completed recovery API/UI.

## Verified provider behavior

The inspected provider is Zitadel v4.15.3, commit
`e2886a61670ca8fd41c9434f87036546e5620bcc`. Findings below were reread with
`git show v4.15.3:<path>`, because the unrelated local source checkout differs
from the pinned tag. That checkout was not edited.

- [Human request conversion](https://github.com/zitadel/zitadel/blob/e2886a61670ca8fd41c9434f87036546e5620bcc/internal/api/grpc/user/v2/user.go#L62)
  passes the requested user ID into `command.AddHuman`. The owner must keep its
  server-selected ID immutable; an ambiguous result is not permission to
  redispatch or choose another ID.
- [User search](https://github.com/zitadel/zitadel/blob/e2886a61670ca8fd41c9434f87036546e5620bcc/internal/query/user.go#L622)
  reads/scans projection rows and only afterwards calls `latestState`. Those are
  separate database operations. A response checkpoint therefore does not itself
  establish one atomic snapshot of the returned rows and checkpoint.
- [List details](https://github.com/zitadel/zitadel/blob/e2886a61670ca8fd41c9434f87036546e5620bcc/proto/zitadel/object/v2/object.proto#L85)
  exposes processed sequence and projection timestamp; these describe projection
  progress, not authoritative account/contact/grant absence or payload conformance.
- Existing isolated-provider fault evidence in the delivery record already
  demonstrated stale active state/contact metadata despite committed source
  mutations. A projection may subsequently advance on another event. Do not
  replace authoritative object-event checks with a "fresh enough" watermark.

## Required distinction before independent approval

Replacement approval only prepares one immutable role-free successor. It must
not claim contact availability, successful source creation, verified contact,
current membership or employment access. Provider uniqueness/conflict outcomes
still need explicit retained handling during the successor's one dispatch.
This does not require enumerating unrelated accounts or proving universal
absence of an email across an organization.

The next source verifier must instead classify the exact frozen provider-user
object with bounded, owner-scoped authoritative history and source reads:

| Evidence | Required handling |
|---|---|
| User lookup absent, but retained creation/state history exists | Absence unproven; do not authorize replacement from the empty projection |
| Exact user present with matching sealed command binding | Reconcile the original creation where valid; retain its original ID/dispatch, never fabricate absence |
| Exact user present with foreign owner/contact/binding or contradictory history | Conflict/manual investigation; no automatic deletion or identity reassignment |
| Exact object consistently absent with authoritative history showing it was never created | Candidate replacement proof for that specific object; this is not a promise that the contact is available |
| Retained removal history | Verify historical ownership/binding before treating removal as a replacement disposition; do not infer it from a 404 |
| Missing permissions, outage, malformed/ambiguous history, source churn or bounded-history saturation | Unavailable, not absent; do not approve automatically |

Closure means only closing the owner's unresolved command, preserving dispatch,
source uncertainty and audit. It must never be presented as source-account
deletion, grant revocation, employment termination or universal access cutoff.
Its approved service disposition still needs implementation and explicit tests.

## Remaining proof gates

Before an independent decision endpoint is exposed: implement/test exact-object
classification against the pinned actual provider, including created-but-stale-
404, contradictory ownership, matching orphan reconciliation, missing/removed
objects, source churn, history bounds and dependency failure. Recheck actual
operator and tenant/product/source scope around the decision transaction; retain
independence across the full root/successor history. Exact and concurrent retries
must retain one decision/successor with no provider dispatch inside approval.

Then connect current-human request detail, source disposition and independent
decision UI through the owning API routes. Run real Admin/recipient/independent-
reviewer acceptance separately from callback/database/provider protocol fixtures.
Do not merge, deploy, tag or close A01–A12 based on this research or a passing
store-only decision test. No provider API, migration or production policy is
invented or activated by this note.
