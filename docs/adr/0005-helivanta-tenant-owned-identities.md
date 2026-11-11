# ADR0005: Fresh tenant-owned Helivanta identities

User-selected model, 2026-11-11. Coordinates Helivanta ADR-0070, operators PR31,
and schema PR1665. Every public signup creates a separate organisation and a
fresh owner, including when the delivery email exists in other organisations.
Do not call instance-wide email lookup and do not adopt a foreign identity by
matching email. The operator supplies a tenant-unique native username.

Public intake retains deterministic tenant/owner IDs, immutable resource/profile,
durable receipt, replay/fencing and20-new-intents/hour capacity bound. The schema
removes cross-tenant email uniqueness; it must be approved/applied before this
worker/intake is released. Links retain native1h TTL,3/hour and10/rolling24h
shared reservations, verification/no-op and no-secret audit evidence.

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
