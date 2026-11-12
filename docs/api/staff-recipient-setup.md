# Staff recipient setup candidate

This extension remains disabled until its schema, HMS consumer and live acceptance
are deployed. Canonical public Go models live in `apps/api/staffcontract/recipient.go`;
`contracts/staff-recovery.ts` is generated. The existing
[Staff identity contract](staff-identity-provisioning.md) retains authority over
creation, source evidence and recovery. Setup grants no business access.

`PUT /v1/orgs/:id/staff-identities/:commandId/invitations/:invitationId`
requires the registered product principal, current canonical/source binding and
the closed body `{"actor_subject":"<initiating actor>"}`. Both IDs are UUIDv7.
Only a confirmed creation without a closed/replaced recovery can receive a link.
The server chooses email or password from native state. Configured accounts
receive no further setup mail. The administrator receives state, phase, expiry
and delivery ID; never the code, raw ticket, recipient email or password.

Recipient workload-only POSTs live beneath
`/internal/v1/customer-signups/staff-invitations/`: `status`, `email`, `password`
and `resend`. Each closed body requires `invitation_id` and `ticket`. `email`
also requires `code`; `password` requires `code` and `password`; `resend` requires
a new UUIDv7 `delivery_id`. Reject unknown, duplicate, case-aliased, malformed,
oversized or phase-inappropriate fields and query parameters. Recipient input has
redacted diagnostics. HMS must expose these through same-origin public auth
routes; ordinary clients must not contact the identity owner directly.

Receipt states: `email_action_required`, `already_verified`,
`password_action_required`, `complete`, `consumed`, `expired`,
`delivery_unresolved`, `proof_unresolved`, `superseded`. `complete` describes credential setup,
not effective product access. Verified email links are redundant. Replaying a
completed password proof never resets credentials. New deliveries supersede old
proof. Lost responses cannot redispatch the same uncertain proof. Status reconciles
native state; a deliberate new delivery retains separate evidence.

Links expire after 30 minutes. Permit five retained dispatch attempts/account per
rolling 24 hours and one delivery/phase per 60 seconds. Email-to-password progression
can proceed immediately after native proof. Rate limits return 429 and conservative
`Retry-After` seconds (60 or 86400). Count attempts before native/Resend calls.
Duplicate delivery IDs return retained state without sending. Provider acceptance
is not inbox delivery. Limit proof to ten attempts/delivery and serialize per
account with a bounded lease. Codes, passwords and raw tickets are not persisted.
State changes and disclosure require audit; dependency failures fail closed.

The additive schema prerequisite is chart 0.3.64 in
[tesserix-k8s #1672](https://github.com/tesserix/tesserix-k8s/pull/1672).
[ADR 0006](../adr/0006-staff-recipient-setup.md) records capacity assumptions and
recovery/acceptance gates. HMS integration, recipient UI, ordinary membership,
reviewed Staff linking, first-reviewer policy and live owner/recipient acceptance
remain open until verified.

## Native account consumer SDK

`apps/api/staffclient` provides scoped creation, retained receipt reads, fresh
observations and idempotent invitation delivery using canonical public Go models
in `staffcontract/identity.go`. Legacy internal type names are aliases, preserving
wire shape. The consumer supplies a dedicated registered product token source;
it never receives the identity service's administrative Zitadel credential.

The SDK accepts only the private owner service destination, disables redirects
and cookies, bounds deadlines/responses and verifies tenant, product, command and
initiating actor in every returned receipt. It does not retry native commands or
cache eligibility. `X-Expected-Product-Key` is an additional rejection guard: the
server compares it to the verified product principal before preparing a command.
It supplies no identity or authority. Existing clients may omit it. Private
provider bodies and token-source errors are never returned to callers.
