# Customer signup email expiry and delivery limits

Status: accepted implementation decision for Helivanta signup.

## Scope and authority

Onboarding owns durable signup intent and delivery reservations; Zitadel owns native email verification, password proofs and their expiry. Resend delivers product mail and cannot extend a native code. Live native email and password generator expiry was verified read-only on 2026-11-11: both are 3,600 seconds. Email and UI explain the one-hour lifetime and fresh-link recovery. No instance-wide policy mutation is included.

Protect identity proofs, recipient privacy and delivery capacity against unauthenticated callers. Browser IDs and public generic responses confer no ownership. Existing native subject/email/org checks, origin checks, workload authorization, proof limits and generic unknown/already-completed responses remain authoritative. Recheck current native email before generating verification codes. Once verified, email-link requests do nothing, consume no allowance and generate no code. Password setup remains a separate explicit action until credential completion has been recorded. Do not reset existing-owner credentials through signup.

## Delivery budget

Retain three reservations per existing one-hour window and add ten per rolling 24 hours, shared by initial mail, verification resends and password setup requests for the same immutable signup. These are attempts, including provider failures: otherwise an outage or retry could bypass the quota. Public responses do not disclose remaining allowance or recipient existence. Existing Global API/IP/proof limits continue separately.

Serialize on the signup row; use its verified tenant ID to read at most ten recent `customer.signup.mail_reserved` audit entries through the existing `(org_id, occurred_at DESC)` index. Reserve the hourly counter and append the daily reservation in the same transaction before native/provider I/O. If audit/database fails, roll back and send nothing. Concurrent requests cannot spend the same last allowance. No new schema, datastore, runtime or infrastructure cost is needed. Historical pre-rollout sends have no reservation entry; their existing hourly counter still applies, and the daily ledger starts at rollout. Failed sends retain their reservation, so retry remains bounded.

The existing intake maximum is 20 new signups/hour. This budget caps new-ledger mail at 200 attempts/day for 20 active signups; it is not an instance-global lifetime quota. Reservations read at most ten rows per signup and write one short audit event per admitted attempt. Existing daily mail per signup is small (at most ten); no throughput/SLO or replica change is claimed. Native/provider calls retain their five-second deadlines. Steady-state API availability is unchanged; loss of identity, database, audit or provider refuses delivery without granting proof. Product target remains normal signup latency dominated by those existing remote deadlines.

## Alternatives and validation

A second TTL in a browser would not revoke native proofs; a shared-instance expiry change would affect other products. New counter columns need a migration and only model fixed windows. The existing append-only indexed audit provides durable rolling accounting without either change. In-memory limits would reset per replica/restart and are rejected.

Verify native generator configuration, verified-owner code suppression, hourly cap, exact 24-hour release, final-allowance concurrency, provider outage accounting, and expired-link UI recovery. Run real PostgreSQL tests in CI; local databases/applications are prohibited by the user. Rollback is the prior application image via normal GitOps; reservations remain attributable. Security-key/U2F sign-in support is a separate login capability and is not implemented by this delivery change.
