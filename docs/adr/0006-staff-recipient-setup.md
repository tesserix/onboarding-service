# Staff recipient setup

The identity owner retains role-free account creation. HMS authorizes the current
human and exact Staff scope; the identity owner rechecks canonical tenant/product
and source binding. Invitation possession permits only proof for the frozen
account. It never grants membership, an employment posting, a Staff/account link,
clinical authority or tenant management.

Planning envelope: 50/150 tenants at months 12/36 and 100 Staff per tenant gives
5,000/15,000 setup records. Assume 10 peak setup requests/s, 90:10 status:write,
at most 8 KiB proof bodies, and five deliveries/account per rolling 24 hours.
This is an assumption, not measured traffic. Reuse existing owner API and indexed
PostgreSQL; no new runtime or infrastructure cost is required. Target availability
follows the existing identity service; bounded synchronous delivery has a 25s
deadline, with provider acceptance distinguished from inbox delivery.

Retain each UUIDv7 delivery before requesting a native code. Store only a SHA-256
hash of a random 256-bit ticket, phase, expiry, attempts and provider acceptance.
Codes/passwords/raw tickets remain transient. Deliver through Resend to the frozen
contact, using the delivery ID as the provider idempotency key. Exact retries never
send again. Lost responses remain unresolved; a new delivery is allowed after
60 seconds, with five attempts per rolling day. Links expire after 30 minutes.
Every new delivery invalidates older proofs. Successful verification is redundant;
password setup never uses a code-free administrative reset. Configured accounts
receive no further setup mail.

Serialize each account's mutations with an owner-row transaction and a 30-second
lease, longer than bounded native requests. Count at most ten proof attempts per
delivery before the native call. Native calls run outside database transactions.
Retain uncertain dispatch and credential outcomes. Reconcile by native contact/
credential observation; do not repeat successful password mutation on response
loss. Dependency or mandatory-audit failures fail closed. Database changes are
additive and application rollback retains evidence. This is request-driven proof,
not a background saga: no polling, durable execution worker or stored user wait.

Threats are stolen invitation links, another tenant's owner, guessed proof and
compromised clients. An opaque ticket is scoped to one frozen account and phase,
requires the independent native email/reset code, and expires. Reauthorize source,
registry and exact ticket before state changes/disclosure. Never return native
codes or tickets to the initiating administrator. Staff recipients set credentials
privately. Owner-only initiation and recipient proof do not waive independent
review of Staff links and business grants.

Acceptance requires real-PostgreSQL concurrency, retry/expiry/budget/audit-failure
tests in CI; native-protocol tests; HMS scope-denial tests; recipient UI; and live
owner/recipient acceptance after GitOps/Kargo deployment. Protocol tests and an
email provider acceptance ID alone do not close that acceptance.
