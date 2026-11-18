# Helivanta returning-account credential recovery

Issue: [Helivanta #1703](https://github.com/tesserix/helivanta/issues/1703).
Status: implementation in progress; no completed recovery journey or deployment.

Zitadel remains the native password and MFA authority. Onboarding owns the exact
customer organization/account binding and private mailbox delivery. Helivanta IAM
owns its application sessions and the interpretation of native credential change
evidence. Workforce owns reviewed Staff/account links. Recovery neither creates
membership nor supplies clinical or operational access.

The existing Staff-identity recovery commands reconcile incomplete account
creation. The signup and Staff invitation capabilities complete first setup.
Neither is a returning-account password or MFA reset API.

## Native password-change evidence

The pinned native source, `tesserix/zitadel` at
`29986e1b8b3e606d20871a2e73874b91f9b5b695`, declares the user's last
`human.passwordChanged` timestamp in `proto/zitadel/user/v2/user.proto`, backed by
the password-change projection in `internal/query/user.go`. Changing a password
does not itself prove that a Helivanta session was revoked. Consumers must assess
the original verified authentication time against current native change evidence.
This avoids treating a local password copy or an optimistic reset response as
credential authority.

The workload-only identity projection accepts `includeCredentialState: true`
only for `purpose: status`. For an active human it returns `credentials` with an
explicit `passwordConfigured` boolean and, when configured, a valid native
`passwordChangedAt` timestamp. Inactive observations retain their existing minimal
shape. Existing requests without the flag retain their exact response shape.
Names, email, hashes, factor secrets and codes are absent from this projection.

A missing timestamp is not proof of a passwordless account. The native adapter
checks the same subject's authentication-method inventory; only an authoritative
inventory without PASSWORD permits `passwordConfigured: false`. PASSWORD without
a date, unknown methods, malformed/duplicate/recased timestamps, invalid human
proof and provider outages are unavailable. The existing strict decoder enforces
required fields and refuses explicit nulls instead of interpreting a missing or
null boolean as a verified false value.

The service verifies the live customer organization/product enrollment before
native I/O and again afterward for this opt-in read. It commits the existing
attributable sensitive-read audit with `credentialState: true` before disclosure.
An out-of-scope customer returns not found; provider, authorization or persistence
failure discloses no partial state. Accepted historical subject-state semantics
are preserved; this observation cannot admit a foreign subject to new membership.

The normal private workload authorization, fixed native destination, bounded
identity read, no redirects and no-store response remain required. Native reads
have a five-second deadline within the service's ten-second deadline. Existing
password-status inventory parsing is reused, rather than adding a second parser.
No new credential, role, service, database table or standing infrastructure cost
is required for this prerequisite. The planning target remains the existing
identity service's 99.9% availability and two-second interactive p99; additional
native lookup cost applies only when the date is omitted and must be measured.

## Remaining recovery implementation

Deliver a dedicated, purpose-bound mailbox challenge for returning accounts,
with native-organization scope from a validated Helivanta login request, expiry,
recipient/source quotas, finite verification attempts and immutable recovery IDs.
Send the same challenge before any account lookup to avoid enumeration. The
verified recipient may then learn whether their selected tenant account is
recoverable or locked. A locked-account message must not be disclosed to an
unverified probe. Password reset uses native proof and native complexity policy;
no password or native proof is persisted or logged. Retain uncertainty after a
lost native response rather than treating password presence as proof that a
particular requested password was accepted.

Helivanta must consume the opt-in native evidence before exposing password
recovery, including stale-session and callback admission tests. Native projection
freshness and consumer cutover/revocation ordering still require integration
verification; this prerequisite is not proof of immediate session invalidation.
The existing setup-link TTL and quotas are unchanged. New recovery limits must
be explicit in its dedicated canonical contract and delivery implementation.

Staff MFA reset additionally requires current tenant-administrator authority,
fresh authentication, the exact reviewed ordinary Staff/account link, a reason
and audit, rejection of self/owner/privileged targets, revocation of old access,
and mandatory fresh enrollment at every applicable login/callback admission gate.
Removing a factor alone is insufficient under a policy permitting password-only
login. Owner access loss retains the verified support/JIT recovery path from
Helivanta ADR-0044; this work does not weaken that policy.

## Delivery and verification

Deploy the owner before an opted-in consumer. A rolled-back owner refuses the
unknown opt-in rather than fabricating evidence; the consumer must fail closed.
Rolling back the opt-in reader does not undo native credential changes. No
migration, password reset, MFA removal or production identity mutation occurs in
this prerequisite. Later schema or credential operations need their own concrete
review and deployment evidence.

Unit tests reproduce the missing native adapter and the decoder accepting an
empty credential object, then assert native evidence, absent-date inventory,
passwordless behavior, strict malformed/duplicate rejection and legacy response
compatibility. Database-backed tests exercise actual enrollment, revocation
during native reads, storage failure and the attributable disclosure audit.
Only the unit suites run locally; database and browser runtime checks use hosted
CI. Complete recovery acceptance still requires actual reset/new-password login,
old-credential denial, MFA reset/re-enrollment, authorized live QA evidence and
the deployed Global API/browser flow before issue closure.

## Dedicated mailbox challenge implementation

Candidate private routes, under the existing exact Helivanta signup-workload
verification and `Cache-Control: no-store`:

- `POST /internal/v1/customer-signups/password-recovery/start` accepts canonical
  `PasswordRecoveryStart`; its tenant must come from the consumer's validated
  native authentication request, never an arbitrary browser tenant selection.
- `POST /internal/v1/customer-signups/password-recovery/verify` accepts canonical
  `PasswordRecoveryProof`. Success acknowledges mailbox proof only. It neither
  signs in nor authorizes a password update or MFA removal.

Strict canonical decoders reject duplicate, recased, missing, unknown, null and
invalid UTF-8 input fields. Start always reserves and sends the same type of
mailbox message before account lookup; absent and foreign native accounts cannot
change this behavior. The code digest is purpose-, command- and browser-bound.
Only digests are stored; raw codes, passwords and native reset proofs are absent.

The separate `password_recovery_challenges` table is prepared in infrastructure
PR [#1712](https://github.com/tesserix/tesserix-k8s/pull/1712), schema commit
`3862a04393146b6639d03d2c2dc469bbb08c3f7f`. CI fetches that exact canonical schema
with SHA-256 `24e3f8a7b4569c46d6e44acf4c32dc6ddd428b5fe4fc334634db83e8827f6b9f`.
This is a test dependency; no live migration is implied or approved by this pin.

Delivery reservations serialize across replicas. Limits are 3/hour and 10/24h
per recipient across tenants, 10/hour and 30/24h per source, and 1,000/hour for
recovery globally. Each code expires ten minutes after reservation and permits
five atomic attempts. A foreign browser cannot spend the recipient's attempts.
Success consumes verification and permits only bounded subsequent access to the
retained verified tenant/email. A repeated command never dispatches again. A new
start for the same tenant, browser and email also sends nothing while its earlier
verified proof is unexpired. A new recovery after expiry is separate from
resending an already verified challenge.

The initial reservation and request audit commit before provider I/O. Resend
uses the retained delivery UUID as its purpose-specific idempotency key.
Acceptance/uncertainty and their audit commit together afterward; a cancelled
request gets a bounded two-second outcome-recording context. A lost response or
crash leaves the reservation retained and does not permit a fresh send under
the same command. A generic `check_email` receipt promises neither provider
acceptance nor inbox delivery. A fresh challenge can be requested within quotas;
there is no retry loop sending newly generated payloads under an old command.
Proof success/failure is audited in the same transaction as its attempt; audit
failure admits no verified capability. Native or mail dependencies are never
called inside a database transaction.

The application role can update only proof attempts, verification, delivery
outcome and update time. A database trigger preserves immutable tenant, browser,
source, recipient, code digest and expiry, prevents attempt resets, prevents
rearming verification and makes delivery outcomes final. Sensitive reads and
later password completion must still reauthorize current native/customer scope.

Remaining before release: hosted PostgreSQL results for these transitions and
quotas; the retained native password-completion command and reconciliation;
bounded personal-data retention with non-reusable command tombstones; Global
API/browser integration and native reset/new-password sign-in acceptance; the
separate Staff MFA workflow. The candidate routes have no public UI consumer yet
and must not be presented as completed recovery. Keep the schema PR unmerged until
its live migration has specific approval after review and database verification.
