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

## Private identity projections and native credential authority

Read-only deployed probes confirmed the Helivanta `IAM_LOGIN_CLIENT` credential
returns403 for native user search and direct user reads. Do not expand it to
user-management privileges. Alongside scoped login-target lookup, add workload-only
`POST /internal/v1/customer-signups/identity-projection` using the existing
identity-management credential and canonical `IdentityProjectionInput` /
`IdentityProjection` Go/Zod contracts. The server validates active Helivanta org,
product enrolment and live native tenant/project proof before reading an exact
subject, and commits sensitive-read audit before returning any projection.

The caller supplies an explicit purpose. `status` returns recognized active or
inactive state only; `membership` additionally assesses active human, verified
email and exact native resourceOwner before admitting any new role; `profile`
returns only the authenticated current user's display name/email. Status/profile
subjects and tenant must come solely from the verified current principal.
Membership target IDs may come from an authorized role-management command. The
verified product workload is trusted to attest its own current principal; no
public target-profile lookup is added. These current-user self reads preserve
accepted legacy memberships without granting foreign subjects new roles.

Status/membership responses never contain presentation PII. Only profile-purpose
responses may contain profile fields, with no new identity master or durable
profile cache. Requests and results bind purpose, tenant, scoped org and subject;
strict canonical decoding rejects duplicate/recased/unknown fields and malformed
state/admission combinations. The consumer limits each response to8KiB.

Only an authoritative native inactive/missing subject becomes inactive. Unknown
tenant/enrolment, refusal, malformed data, native outage or audit failure are
unavailable, never a fabricated inactive result that revokes a clinician's
credentials. Profile absence remains explicitly unavailable. The service audits
opaque subject/purpose, without names, email, password, token or factor data.

### Local consumer acceptance dependency

Helivanta's browser CI uses the same private identity-read handlers and native
projection adapter as production. The separate `helivanta-identity-fixture`
command is compiled only with the `identityfixture` build tag; the production
API image neither contains nor enables it. It requires an explicit fixture flag,
the exact local native issuer, development database credentials and an isolated
`onboarding_fixture` database on a loopback address. It binds only to
`127.0.0.1:18787` and accepts a committed synthetic workload credential only in
this separately built command. All signup, password and policy routes are
unavailable through the fixture listener.

The consumer seeds two synthetic native organisations, metadata and project
grants, then supplies public project/organisation identifiers in a manifest.
The fixture verifies live native scope before recording its isolated projection
and enrolment. Every read still traverses the normal workload route, active
product/tenant/enrolment proof, live native adapter and durable audit ledger.
This does not simulate successful onboarding or email delivery. Production
retains its projected workload token and private service destination.

Native missing-user evidence is explicit: an empty/HTML/generic gateway404 is
unavailable, not inactive. The adapter recognises the pinned native GetUserByID
`scanUser` not-found detail (`zitadel.v1.ErrorDetail`, `QUERY-Dfbg2`, gRPC code5)
before accepting missing-user state. This is verified against native fork
`29986e1b8b3e606d20871a2e73874b91f9b5b695`. Unknown or changed error contracts
fail closed and require review during native upgrades; HTTP status alone must
never globally revoke a valid account. The browser fixture preflight checks this
against an actual missing synthetic native subject.

### Owner-bound verification progress

`GET /internal/v1/customer-signups/:id/users/:userID` is a workload-authenticated,
read-only projection. It requires the private receipt hash and an exact match to
that signup's immutable owner subject. A missing/foreign receipt or owner returns
404; the read never consumes a native proof, issues mail or changes credential
state. The existing unfiltered receipt route remains available for setup progress.
Consumers must use the owner-bound route when interpreting a particular email link.
