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
