# ADR 0004: Durable role-free Staff identity provisioning

Status: implementation in progress. Provider protocol, durable ledger and exact
receipt/observation endpoints have local tests. Creation submission, bounded
history, operator recovery and consumer UI remain open. No live provisioning
authority is established by this document. Consumer:
`tesserix/helivanta#417`, `#1053` and the A01–A12 Staff acceptance plan.

## Ownership, scope and budget

Onboarding owns privileged identity-provider operations and the canonical
tenant-to-Zitadel-organisation mapping. Helivanta owns the current human Admin
decision, Staff records, membership and operational authorization. Creating a
human account must assign no organisation owner/member role, project role,
Helivanta membership, employment, hospital grant or clinical privilege.
`Service.AddMember` defaults to `ORG_OWNER`; it is not the Staff account API.

Use the explicitly configured, canonically registered product machine and its
current active entitlement. `hms` is a test fixture, not a production default.
Resolve the production key through canonical onboarding under owner issue #12;
do not infer it from branding or alias it to `helivanta`.
Resolve the source organisation through the same fresh owner verification as
`ProductIdentity`, before dispatch and before releasing a current observation.
Browser input cannot select an IdP organisation or product, claim a verified
contact, supply a password, or request roles. A product is accountable for its
human actor claim; machine authentication alone does not prove that human's
current Helivanta authority. Helivanta must check that authority before each
request and again when accepting the owner result.

Planning assumptions, requiring measurement: 150 tenants, 20 hires per tenant
per month = 36,000 commands/year; 512 bytes of receipt metadata is about 18 MiB
per year before private input, audit and indexes. Allow bursts of five commands
per second and ten observation reads per second, with a maximum 4 KiB command
and 2 KiB public receipt. A single current PostgreSQL writer is sufficient.
Target 99.9% API availability; ordinary receipt reads target p99 300 ms and
provider observations have a two-second total deadline. Provider creation may
remain unresolved after that deadline; HTTP success must not invent certainty.
No additional service, broker or fixed infrastructure is proposed.

The Staff identity product key is explicitly configured as
`STAFF_IDENTITY_PRODUCT_KEY`, with no production default. Current private
receipt reads have a three-second service budget; observations have six seconds
including repeated current registry/source verification, with two seconds for
the provider user/metadata reads. These are bounds, not measured latency SLOs.

## Provider evidence and retry constraint

Versioned primary source inspected on 2026-10-30: Zitadel **v4.15.3**, also the
observed local provider version. Production version/configuration needs separate
verification.

- `proto/zitadel/user/v2/user_service.proto`: `POST /v2/users/new`, optional
  immutable `user_id`, response `id`, and `user.write` capability.
- `internal/api/grpc/user/v2/human.go:createUserTypeHuman` calls
  `command.AddUserHuman`; it does not implement an idempotency-key ledger.
- `internal/command/user_v2_human.go:AddUserHuman` reads the ID's existence and
  rejects it only when `isUserStateExists` is true.
- `internal/command/user_model.go:isUserStateExists` excludes deleted and
  unspecified states. `user_v2_model.go:Query` always includes removal events;
  reduction sets the state to deleted. Thus a fixed caller-selected ID alone is
  not evidence that an uncertain create can safely be repeated after deletion.
- `internal/repository/user/human.go:HumanAddedEvent.UniqueConstraints` declares
  username uniqueness, not an enduring caller-key receipt.

These source facts reject blind automatic create retries. They do not claim an
end-to-end live demonstration of ID reuse; provider persistence may impose
additional constraints. Do not depend on undocumented constraints for safety.

Source links use this base:
<https://github.com/zitadel/zitadel/tree/v4.15.3>.

## Durable command and recovery

The first command also reserves its case-insensitive contact within the verified
provider organisation. Persist an append-only root contact claim in the schema
owner, atomically with preparation and audit. Its original command, source org
and original email are retained by a composite foreign key. The key uses Go
`strings.ToLower`, matching the inspected v4.15.3 event-store username constraint
normalization; do not strip dots/plus suffixes or rewrite the frozen email.
This is an owner command reservation, not an email-based identity match or an
assertion that another source organisation permits that login. Provider policy
can impose narrower/global uniqueness and must still be honored.

Different commands cannot acquire the same root claim, including after uncertain
dispatch or upstream deletion. Exact retries recover the original random fields
and reservation. Existing commands without a retained claim fail closed for new
dispatch and require reviewed classification; do not silently backfill historical
facts. Root claims cannot be released by expiration or rewritten/deleted. A later
operator-approved replacement must explicitly reference that root and preserve
its prior command/outcome history; it must never be mistaken for a retry of the
old dispatch. That operator replacement protocol remains an acceptance gate.

Persist immutable command input, tenant, product machine, human actor, UUIDv7
command ID, required reason, server-selected random provider ID and a random
binding nonce **before** provider I/O. Do not use the existing expiring 24-hour
idempotency cache as the command authority. Commands and retained receipts must
survive retries, process restarts and membership removal.

Commit a single dispatch claim under a short database transaction with current
registry checks and audit. Never hold that lock while calling Zitadel. Only the
winning claimant can send one creation request. No transport/application retry
may repeat the create after dispatch has become possible. Freeze the source
organisation and exact ID in that request; attach the nonce as source metadata.
Let the provider send contact-verification/account-setup messages. Do not return
codes, links, passwords or raw provider bodies to the product or its logs.

An exact replay returns the retained command or performs a bounded **read-only**
reconciliation; a changed actor or input conflicts. Concurrent requests cannot
claim dispatch twice. A lost create response, timeout, process crash after claim,
or failed receipt persistence produces a visible unresolved state. A 404 after
uncertainty is not permission to create again: it cannot distinguish never sent,
not yet visible and created-then-deleted.

Reconciliation must match provider ID, organisation, creation provenance,
original contact and binding nonce before recording a created identity. The
nonce is correlation, not authority against a compromised IdP administrator.
Changed/missing/ambiguous evidence remains unresolved; a missing or disabled
identity never becomes verified eligibility. Provider contact verification and
account setup are separate observations from account creation and delivery.

A crash before the claim permits normal dispatch on retry. A crash after claim
requires observation or attributable owner/operator resolution. The operator
path must preserve the command, evidence and uncertain outcome; it cannot clear
the dispatch marker or resend the same provider ID. New-command replacement
needs an explicit reason and conflict/orphan reconciliation, with no account
activation implied. This recovery path must be implemented and exercised before
calling provisioning complete; permanent unexplained pending commands are not
an accepted end state.

Keep immutable creation receipts distinct from fresh eligibility observations.
An old successful receipt cannot authorize current membership if the source was
later disabled, deleted, changed or lost its tenant/product binding. Existing
global identities require a separate reviewed resolve/binding journey; matching
email alone must not disclose or enroll them. Helivanta must classify historical
Staff links/grants before admitting a legacy identity as a first enrollment.

## Persistence and release gates

### Provider projection-failure correction — 2026-10-31

A separate local v4.15.3 provider reproduced successful deactivation (sequence
4) followed by two HTTP 200 active/verified reads at sequence 2, with matching
nonce metadata at sequence 3. A per-synthetic-user UPDATE trigger failed the
user projection while permitting SELECT. The actual owner observation method
returned active/verified with no error in 102 ms. This closes the uncertainty
about reproducibility; repeated projected reads cannot establish current proof.

Add an exact filtered event-store read after the final projected read, within
the existing two-second budget. The v4.15.3 admin `ListEvents` implementation
uses `AwaitOpenTransactions`, exact aggregate/resource-owner filters and event
types. This requires existing owner `events.read` capability; absence fails
closed, without requesting credential expansion. Fetch only state, email
verification/change and metadata mutation events after the lesser observed
revision, with a 32-event/64-KiB bound. Saturation, malformed/foreign evidence,
or an unreflected relevant mutation yields unavailable. Inspect metadata keys
so unrelated metadata does not invalidate a nonce; omit password/login events
and never disclose event payloads or editor details. Initialization events can
change state without advancing the user row revision, so compare their final
state explicitly rather than require an impossible revision catch-up.

This boundary detects committed relevant user events; it is not a lock against
subsequent changes. Current organisation/registry proof, real recipient setup,
missing/denied-event-store tests and consumer command-boundary reauthorization
remain required. The filtered request and behavior must be verified against the
actual pinned provider before accepting the correction.

Extended real-provider testing reproduced a second failure: after 15 failed
projection reads, deactivation sequence 14 was skipped. A later unrelated
profile edit advanced the row to sequence 15 while retaining active state.
The initial newer-than-row event check incorrectly returned active/verified.
The handler's `handleFailedStmt` permits continuation at `maxFailureCount`.
Therefore the final design also reads latest authoritative state/email-change/
email-verification events with exact filtered limit-one queries and compares
their facts/order directly. The skipped-deactivation regression failed before
this correction; the same actual fixture then denied current proof in 135 ms.
After an explicit synthetic source reactivation at sequence 16, the final owner
method returned active/verified in 227 ms. These are local observations, not SLOs.

Canonical SQL belongs in `tesserix-k8s`,
`charts/apps/db-schema-bootstrap/schemas/global/global/onboarding_db.sql`, with
an append-only upgrade and SQL provenance. Do not change historical command
meaning or remove receipts on credential/entitlement loss. Scope every command
lookup by current authenticated product and canonical tenant; protect private
contacts and audit reads as well as writes. No browser or product SQL access.

Real PostgreSQL tests must prove immutable facts, exact/conflicting replay,
single concurrent dispatch, no send before commit, entitlement withdrawal during
lock wait/provider I/O, audit rollback and retained uncertainty after restart.
HTTP provider tests must cover wrong ID/org/contact/nonce, malformed/oversized
responses, redirects, timeouts, conflict, lost response and later deletion;
verify there is no second create. Actual local provider/UI acceptance must cover
Admin initiation, recipient contact verification/account setup, independent
Staff linking/review and zero operational permission before approved grants.

Persist owning-repository SQL and contracts before consumer integration. Run
full owner Go race/vet/lint/build and actual database tests without skipped store
coverage. Deploy through existing GitOps only after exact source/schema approval;
rollback must retain dispatch facts and prohibit duplicate creation. Helivanta
must remain fail closed if the owner route/configuration is absent. No migration,
credential rotation, release or production activation is authorized by this ADR.
