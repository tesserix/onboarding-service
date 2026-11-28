# Native Staff MFA recovery admission

Status: implementation decision for Helivanta #1703; unreleased.

## Boundary

Use the existing private Helivanta customer-workload lane, not a browser route.
HMS first verifies the native ID token, exact subject/signed sid and current login
tenant membership. It submits the subject, current generation, login tenant,
request ID and verified signed sid to
`POST /internal/v1/customer-signups/staff-mfa-recovery/admit`. The exact customer
workload is authorized by the existing private route middleware; a browser cannot
supply these assertions. The HMS consumer implementation remains a release gate.

The identity owner verifies the current login tenant and the credential owner's
original tenant/product/native scope. The latter can differ for a legacy
same-subject membership. It verifies the exact current generation, current owned
active verified human account, latest post-removal verified enrollment, and the
exact native session's independent primary proof and proof of the factor kind
just enrolled (TOTP or security key, helivanta#1905). Password proof must also
postdate the current native password change. Re-read enrollment after session
I/O and recheck account eligibility, native password and current scope before
committing admission. An unconfirmed write or preparation cannot be admitted.

Workforce reviewed-target versions and ordinary-account protection authorize
reset preparation, dispatch and administrator receipt disclosure. They do not
authorize authentication. Requiring the historical Staff version or excluding a
subsequently promoted administrator at login would lock out a valid account after
a transfer or role change. Admission grants no Staff, employment, department or
operating authority; those depend on current HMS authorization.
Completion and sensitive admission audit commit together under the global account
generation lock. The initial verified enrollment is immutable after completion;
later logins can prove a newer valid enrollment without rewriting that history.
HMS rechecks current credential generation and tenant membership after
the owner response before signing an application session. A concurrent reset wins
through the owner's generation lock and HMS's protected-entry generation checks.

## Failure and privacy

Known absent/stale/wrong session or unenrolled proof requires authentication;
missing/malformed/unavailable native/scope/audit evidence is unavailable. Neither
case produces a generation or cookie. HTTP is outside database transactions;
no native write is performed or automatically retried. Keep exact native timestamp
precision and ordering. Never persist or return native sid, session token, factor
secret/code, or administrator/reason source data in an admission receipt.

The authenticated customer workload is the admission audit actor; the native user
is the checked subject. It must not impersonate the original reset administrator
or product-machine actor. Existing preparation/removal audit remains unchanged.

## Delivery

Use the existing services/database and the recovery planning envelope: 10 peak
recovery commands/second, 50/150 tenants, 99.9% availability. Private calls have a
bounded 30-second command budget and native reads a five-second ceiling. These are
planning assumptions, not measured SLO evidence. No new standing infrastructure
cost or schema is required. Retain completed history on rollback; never remove a
fence as rollback. Schema/owner precede HMS release, with named live migration and
QA factor-reset approval. Full HMS reset source guards, forced-enrollment UI and real
native/browser acceptance are required before deployment or closure.

Rejected: trusting completed status or auth_time as current-session proof;
performing native I/O while holding database locks; exposing the original private
admin command snapshot to a browser; or stamping completion with the old actor.
