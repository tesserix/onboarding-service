# ADR0005: Fresh tenant-owned Helivanta identities

User-selected model, 2026-11-11. Coordinates Helivanta ADR-0070, operators PR31,
and schema PR1665. Every public signup creates a separate organisation and a
fresh owner, including when the delivery email exists in other organisations.
Do not call instance-wide email lookup and do not adopt a foreign identity by
matching email. The operator supplies a tenant-unique native username.

Public intake retains deterministic tenant/owner IDs, immutable resource/profile,
durable receipt, replay/fencing and20-new-intents/hour capacity bound. The schema
removes cross-tenant email uniqueness; it must be approved/applied before this
worker/intake is released. Links retain native1h TTL and each workspace's
3/hour and10/rolling24h reservations, verification/no-op and no-secret audit evidence.

An active, unbound historic existing_account intent resumes creation of its own
fresh owner through the same deterministic claim. Expired intents do not resume.
No original identity/key or already accepted binding is deleted, moved or copied.
Existing accepted binding delivery/recovery remains compatible; new public native
continuation accepts only the exact immutable freshly created owner, verified in
its target organisation. Foreign accounts fail before native adoption.

Ready workspace sign-in is pinned to the target native organisation by the
consumer. Email lookup for login must be scoped by that native auth request.
A same-email hospital and pharmacy owner have independent passwords and MFA.
Owner policy operations retain current native ORG_OWNER/tenant/product proof,
revision checks, fresh authentication, append-only audit and operator ownership.
Current policy remains mandatory MFA/no grace, including external sign-in.

No new service/store. Reuse existing intake12-second synchronous budgets and
bounded retry worker. Native outages remain unavailable/pending. Lost remote
responses reuse deterministic IDs; conflicts never overwrite native binding.
Rollback retains expanded schema after duplicates are accepted; recovery is
forward and must not delete accepted tenant identities to restore an index.

Validation requires real PostgreSQL duplicate-email intake/replay and historic
receipt recovery, native boundary rejection and operator lost-response tests,
then Helivanta scoped login/UI and real deployment acceptance. PostgreSQL runs in
CI; local applications, DB servers and containers are not started.

## Private tenant login lookup

Live read-only checks proved Helivanta's login-client PAT cannot ListUsers (403).
Do not expand that credential. POST `/internal/v1/customer-signups/login-target`
uses the existing exact Helivanta API workload/audience and mesh boundary. Its
canonical generated LoginTargetInput contains tenant UUID, bounded email and request
UUID; LoginTarget returns only tenant/org/subject. No browser route exposes it.
The caller derives org from the active native OIDC request pinned to its web
client, applies its existing password-attempt limit/equalised refusal and never
passes a password to this broker.

Onboarding checks active Helivanta-owned organisation and product enrolment,
current native tenant/project proof, and exactly one active email-verified human
inside that organisation. Audit must commit before returning the binding.
Unknown/ineligible target is404; invalid bounded input400; missing/wrong workload
401; native/store/audit failure503. This is a sensitive read, not account creation
or authentication. It has a10-second total budget, a5-second native lookup and
at most2 candidates. No retry/cache/new service is added. Helivanta checks the
actual password session's subject/org before proceeding to MFA/callback.

## Recipient-wide delivery budgets

A fresh signup receipt cannot reset delivery abuse controls. Retain each immutable
workspace's3/hour and10/rolling24h reservations, including failures, and additionally
limit the same immutable recipient hash across tenant intents to10/rolling hour
and30/rolling24h. No email address, hash or remaining allowance is exposed in a
public response or audit detail. A transaction-scoped recipient advisory lock
serializes different signup rows before reading a bounded30-event audit window
and appending the reservation. No remote call runs inside that transaction. A
non-unique recipient/tenant index supports the join; it conveys no identity,
account-adoption authority or cross-tenant access. Distinct tenant owners retain
independent verification/password/MFA despite sharing a delivery budget.
