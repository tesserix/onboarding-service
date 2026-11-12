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

The candidate source verifier now classifies the exact frozen provider-user
object with bounded, owner-scoped authoritative history and source reads. Its
reader view is implemented; independent decision integration remains open:

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

The current adapter proves absent, matching and removed states against the actual
isolated v4.15.3 provider. Immediate matching proof failed closed when asynchronous
email delivery advanced the account head; a fresh read succeeded without another
creation. The fixture now bounds fresh read attempts at three and dispatches
creation once. Matching requires the sealed nonce and retained birth/contact;
removal additionally requires actual 404s, terminal removal, one human birth and
the latest causal nonce binding in bounded ownership history. No source mutation
is performed by classification.

An explicit projection-fault test captures one synthetic account's parent and
cascading child rows privately before temporarily removing only its projection.
Actual GetUser then returns 404 while history retains creation; recovery refuses
absence. Restoration recomputes generated columns, checks all captured rows and
recovers matching proof. The initial restoration attempted to insert a generated
notification column; it failed, was corrected, and the captured state was fully
restored before the successful rerun. Source event history was preserved.

These are actual-provider protocol/fault tests plus TLS and database/HTTP callback
tests, not normal Admin/recipient/reviewer UI acceptance. The new fresh source
view reauthorizes current human/tenant/product scope and commits disclosure audit;
it does not reconcile creation receipts or approve recovery.

Before an independent decision endpoint is exposed: integrate the tested exact-
object classifier with matching orphan receipt reconciliation and explicit
conflict handling. Contradictory ownership, missing/removed objects, source churn,
history bounds and dependency failure must continue to fail closed. Recheck actual
operator and tenant/product/source scope around the decision transaction; retain
independence across the full root/successor history. Exact and concurrent retries
must retain one decision/successor with no provider dispatch inside approval.

Then connect current-human request detail, source disposition and independent
decision UI through the owning API routes. Run real Admin/recipient/independent-
reviewer acceptance separately from callback/database/provider protocol fixtures.
Do not merge, deploy, tag or close A01–A12 based on this research or a passing
store-only decision test. No provider API, migration or production policy is
invented or activated by this note.
