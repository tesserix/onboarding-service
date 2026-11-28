# Helivanta Staff authenticator recovery

Work for [Helivanta #1703](https://github.com/tesserix/helivanta/issues/1703).
Private workload command endpoints are implemented. The companion HMS branch
contains authorization, revocation/admission and enrollment UI work, but native
end-to-end acceptance and deployment remain unverified. This is not a released
Staff recovery journey.

Workforce owns the reviewed Staff/account link and exact employment/link versions.
IAM owns current administrator authority, ordinary-target protection and application
revocation/admission. Onboarding owns the managed native account binding, durable
command, native writes and evidence. The browser supplies a command ID, expected
reviewed link/version, Staff version and reason. It cannot select a native subject,
claim administrator authority or assert that application revocation happened.

## Canonical contracts

`apps/api/customercontract/staff_mfa_recovery.go` defines the browser intent and
private workload target/request. `go run ./cmd/customercontract` generates the
Zod schema in `contracts/customer-signup.ts`; `-check` verifies parity.
Both sides require UUIDv7 command/link selectors, positive safe integer versions,
bounded clean reasons and strict JSON objects. Duplicate, recased, null and unknown
fields are rejected. Hospital, pharmacy and supplier employer kinds remain
distinct. Shape validation supplies no native ownership, permission, active
employment or reviewed-link authority.

## Durable lifecycle

The private API is scoped to the authenticated, currently registered Helivanta
product machine and the tenant's active product/native organization grant. A
tenant-owner token, console human, another product or a client header cannot
replace that workload authority.

| Method | Path under `/v1/orgs/{tenant}/staff-mfa-recoveries/{command}` | Body/result                                                              |
| ------ | ------------------------------------------------------------- | ------------------------------------------------------------------------ |
| `PUT`  | Base path                                                     | `StaffMFARecoveryRequest`; retains preparation only                      |
| `POST` | `/dispatch`                                                   | `StaffMFADispatchRequest`; matching committed application fence required |
| `POST` | `/cancel`                                                     | Exact original `StaffMFARecoveryRequest`; prepared only                  |
| `GET`  | Base path, exactly one `actor_subject` query                  | Private `StaffMFARecoverySnapshot`; never implicitly prepares            |

Mutation responses use the minimal canonical `StaffMFARecoveryReceipt`. Processing
or uncertain dispatch returns 202 with retained state; acknowledged removal still
waits for enrollment and is not completion. Requests are strict, bounded to 8 KiB
and must match tenant/command path IDs. Responses are `no-store`. Generic replay
caching is excluded so every retry reauthorizes current entitlement and exact
intent. The private snapshot contains original source/reason for backend recovery;
it must not be forwarded wholesale to the browser.

`apps/api/staffclient` exposes `PrepareMFA`, `DispatchMFA`, `CancelMFA` and
`GetMFA` using these canonical contracts. It requires the Helivanta product and
exact tenant, command and actor scope before sending a mutation. Returned receipts
must match the command; private snapshots must additionally match tenant and
actor. Responses are bounded and strictly decoded. The existing fixed internal
owner destination, workload token, timeout, cookie exclusion and redirect refusal
apply. An uncertain transport outcome is returned without an automatic write
retry; consumers recover through the retained command rather than issuing a new
reset. These client methods do not supply HMS administrator authorization or
prove a committed application fence.

The application fence is an authenticated HMS assertion, not proof supplied by a
browser. HMS must commit actual idempotent revocation before invoking dispatch and
verify fresh administrator/ordinary-target and reviewed Workforce source authority.
Owner native checks additionally reject foreign/unverified/non-human/passwordless
accounts and any native organization member. That exact member query is bounded
to the target user, rather than treating a truncated member list as absence.
The pinned Zitadel v4.15.3 member search can serialize an empty result as
`{"details":{"viewTimestamp":"<RFC3339 timestamp>"}}`: ProtoJSON omits zero
`uint64` count and sequence fields. Hosted HMS run `44517575218` observed this
shape for both isolated hospital and independent-pharmacy targets. Requiring a
positive sequence incorrectly returned unavailable before preparation.

An omitted or explicit canonical zero sequence is accepted only for a zero count,
an empty or omitted result list, and a valid non-future positive view timestamp.
Missing/invalid/null timestamps, malformed/recased/duplicate metadata, contradictory
counts/results and nonzero member results without a positive sequence fail closed.
A known native organization member remains ineligible. The current owned/active/
verified/password account checks, exact target query, workload authority, five-second
native timeout and reauthorization around preparation/dispatch still apply.

Primary sources: [ListDetails fields](https://github.com/zitadel/zitadel/blob/v4.15.3/proto/zitadel/object.proto#L66),
[member query and scoped projection](https://github.com/zitadel/zitadel/blob/v4.15.3/internal/query/org_member.go#L71),
[member response conversion](https://github.com/zitadel/zitadel/blob/v4.15.3/internal/api/grpc/management/org.go#L265),
and [list metadata conversion](https://github.com/zitadel/zitadel/blob/v4.15.3/internal/api/grpc/object/converter.go#L84).
These source and regression checks do not prove completed native recovery or live
release; HMS native acceptance remains required.

TOTP or security-key presence is only a pre-dispatch check, never proof of new
enrollment. A target with neither cancels a prepared command without a native write.
Dispatch removes every security key (any state) and then the TOTP, and re-reads
presence. Any failed, lost, out-of-order or incomplete step leaves the command
`unconfirmed` rather than `awaiting_enrollment`; the last acknowledgement is the
removal baseline (helivanta#1905). Other factor kinds are left in place.

Native HTTP runs outside database transactions. Acknowledgement or uncertainty is
retained in an independent bounded transaction even if the caller disconnects or
entitlement is withdrawn. Disclosure is reauthorized after I/O; no native retry
follows lost acknowledgement. Per-attempt audit correlation remains separate from
the immutable command ID. Completed admission, source checks, forced enrollment
and HMS session enforcement still require the remaining consumer integration.

The additive table is in
[tesserix-k8s #1713](https://github.com/tesserix/tesserix-k8s/pull/1713).
`scripts/ci/setup-postgres.sh` pins its exact source and SHA256 for disposable
hosted databases. This is not a live migration approval.

| State                 | Meaning                                                | Permitted next state                             |
| --------------------- | ------------------------------------------------------ | ------------------------------------------------ |
| `prepared`            | Immutable account/link intent and audit retained       | `processing`, `cancelled`                        |
| `processing`          | One dispatch claimed after matching application fence  | `awaiting_enrollment`, `unconfirmed`             |
| `awaiting_enrollment` | Native removal acknowledged; access still gated        | `completed`                                      |
| `unconfirmed`         | Native write outcome is uncertain                      | Separate authorized operator resolution required |
| `cancelled`           | No dispatch; ID reserved against late preparation      | None                                             |
| `completed`           | Enrollment/session admitted; generation still required | New reset needs a new command                    |

Each local transition and audit commit together, without HTTP inside transactions.
Preparation/dispatch/cancellation check the current registered machine, native
project, organization and product entitlement. Frozen source includes tenant,
employer kind/ID, Staff/link IDs and versions, account, originating actor and
reason. Changed-input replay fails. Service/IAM source reauthorization remains
required around I/O; the stored command supplies no new authority.

The account fence never expires into another write. Concurrent dispatches return
one permission to send; replay returns retained state. Missing-command cancellation
reserves a tombstone against late preparation. Acknowledged/uncertain writes cannot
be cancelled. Outcomes remain recordable after entitlement loss, but disclosure
and admission require fresh scope authority.

Completed records remain in latest-generation admission reads. A newer preparation
prevents an older receipt from completing again. Prepared cancellation exposes
the preceding completed requirement; IAM session revocation is never undone.
Open-command uniqueness, the account lock and completion's latest-generation
check use the exact native subject across tenant memberships. Tenant ownership
and command/source disclosure remain scoped to the originating tenant and actor.
See [subject-wide admission](adr/0008-subject-wide-mfa-recovery-admission.md).
Canonical native timestamps remain text so nanoseconds survive PostgreSQL.
Restricted column grants and transition guards prevent source rewrites, history
deletion and illegal state jumps by the application role.

The private customer identity projection accepts `includeMFARecovery: true` only
with `includeCredentialState: true` and the `status` purpose. For an active exact
subject, `mfaRecovery` explicitly reports `none`, `blocked`, `enrollment_required`
or `completed`. Every non-`none` state carries `generationId`, the command UUID.
This account-wide projection intentionally covers legacy same-subject tenant
memberships, but reveals no originating tenant, employer, actor, reason or native
evidence. Old callers retain their original shape. Inactive subjects receive no
new recovery projection. Current tenant entitlement and native scope are checked
again after source I/O, and disclosure requires its opt-in audit. Storage or audit
failure is unavailable, never `none`. HMS still needs to opt in and enforce the
returned requirement; a completed command does not prove the current session's
fresh primary/factor evidence or admitted generation.

## Native evidence and remaining wiring

Native research pins `tesserix/zitadel@29986e1b8b3e606d20871a2e73874b91f9b5b695`.
Removal needs exact acknowledged owner, sequence and change instant. A missing or
malformed response is uncertain and never automatically retried. Latest lifecycle
events distinguish new TOTP or security-key enrollment verification from ordinary
code checks and key sign counts. Session proof matches the verified signed `sid`,
user and organization, with independent fresh password timestamps and a fresh
timestamp for the factor kind just enrolled (`totp` or `webAuthN`). Neither factor presence nor latest
factor `auth_time` proves enrollment or fresh primary authentication. Native reads
are bounded and scoped; redirects and cookies are disabled. The ledger stores no
credential, factor secret/code or native session token.

The private customer-workload admission route is
`POST /internal/v1/customer-signups/staff-mfa-recovery/admit`. Its strict input is
`{scope: {tenantId, subject, generationId, requestId}, nativeSessionId}`. The HMS
server must verify current login membership and obtain the sid from its verified
native ID token before calling; the browser cannot assert them. The owner checks
both current login and original credential-owner tenant/product/native scope,
latest acknowledged generation, current owned active verified human eligibility,
post-removal enrollment, exact independent native password and enrolled-factor proof, and current
password-change cutoff. It rechecks eligibility, enrollment and generation after
native I/O. Completion and admission audit commit under the global account lock;
a later valid enrollment does not rewrite first-completion history. Only the
admitted generation is returned, without Staff or native-session data.

Admission does not depend on historical Staff versions or prohibit a subsequent
administrator promotion. Those rules protect reset authorization and admin receipt
disclosure. Current HMS authorization still controls every employment, department
and operating capability; authentication creates none of them.

Still required: fresh administrator/ordinary-target authorization and exact current
reviewed Workforce source at reset; pre-native IAM revocation; forced new
enrollment under permissive policy; enrollment UI; HMS native-admission consumption
and membership/generation rechecks before signing; and every protected admission
path. A private implementation is not a released recovered-login journey.

## Validation and release

Unit tests cover strict Go/generated contracts and native dependencies. Store
tests use disposable PostgreSQL in hosted CI: concurrency, changed/foreign source,
audit rollback, cancellation tombstones, timestamp precision, generations and the
restricted application role. Local checks compile those cases but do not start
a database. A skipped local database test is not runtime evidence.

Obtain named live schema-migration and QA credential-reset approval after reviewable
code and runtime gates are ready. Apply additive schema before owner/consumer
release; retain history on code rollback. Complete hosted native-browser and
deployed acceptance before enabling the UI or closing #1703. No new standing
service, worker, pod or external credential store is introduced.


## Isolated native acceptance fixture

The build-tagged `helivanta-identity-fixture` command remains read-only by default.
`-staff-mfa-recovery` is a separate explicit opt-in for disposable hosted CI. It
requires the exact local Zitadel issuer and existing guarded development database.
The manifest must identify a dedicated active native machine owned by the platform
organization; startup verifies that account with Zitadel before registering it.
Real native JWKS verification and the Helivanta project audience authenticate
product workload calls. Human tokens and a substituted machine are rejected.

The baseline Tenant A/B identity reads stay available. The opt-in additionally
provisions two fixed UUIDv7/RFC 4122 tenants, `11111111-1111-7111-8111-111111111111`
and `22222222-2222-7222-8222-222222222222`, with separately verified native
organizations. Only these two canonical scopes admit MFA recovery commands;
the older non-RFC sample IDs are not passed to the workload recovery client.
Exact identity reads, UUIDv7 recovery command reads/preparations, explicit
dispatch/cancel commands and the private admission endpoint are exposed
in this mode. Other tenants, methods and paths stay unavailable. The fixture wires
`WithStaffMFARecovery` to the real native adapter; it does not replace factor
removal, enrollment, session proof, authorization or audit with browser mocks.
This fixture capability alone is not evidence of successful native acceptance.

Focused checks: `go -C apps/api test -race -tags identityfixture
./cmd/helivanta-identity-fixture` and the matching `go vet`/`go build` commands.
No live tenant, credential or factor is changed by these fixture unit checks.


The isolated manifest distinguishes `machineUserId` (native JWT subject and
registered product authority) from `machineClientId` (the native machine's
preferred OAuth login name). The latter is optional metadata consumed by the
companion HMS fixture; it never replaces the subject used to authorize a product
command. Pinned Zitadel v4.15.3 `GenerateMachineSecret` returns the preferred login
name as `clientId`. Native token verification continues to bind the actual JWT
subject to the registered machine account.

## Bounded unavailable diagnostics

Preparation emits only its fixed stage when identity availability fails. The
native target adapter separately emits a fixed proof stage. Neither warning
contains account, tenant, command, actor, reason, credential or provider body.
The isolated acceptance command injects a JSON logger so the consumer CI can
select these fields and the normalized prepare-route HTTP status. This is
operational failure evidence, not authorization or successful native acceptance.

The diagnostic regressions failed before logging was added, then passed. Local
validation passed the complete onboarding/native-adapter race suites, the
identityfixture race suite, vet, Go build, golangci-lint and customer-contract
check. No local application, database or container was started. The latest
consumer native run at HMS `d6a8430a` still reported one pass and two 503 failures;
its hospital/pharmacy recovery journeys remain unverified.

### Native admission follow-up

Consumer `960ca294` proves both real employer models now acknowledge native
factor removal, reject old access/renewal, require a different replacement factor
and reach callback after its confirmation. Both cases then fail to reach the
dashboard, with unavailable current-credential evidence; the run still reports
one pass, two failures and 107 cases not run. Recovery is not accepted.

Admission and its native enrollment/session reads now emit only fixed unavailable
stages. No provider response, account, native session, generation, timing value
or factor data is logged. Existing proof/cutoff and rejection behavior is unchanged.
The admission/native diagnostic privacy regressions were observed failing before
logging, then passing; complete onboarding/native race suites, vet, Go build,
golangci-lint and contract checks passed locally without apps, databases or
containers. Hosted native callback/admission acceptance remains outstanding.
