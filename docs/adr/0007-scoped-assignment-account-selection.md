# Scoped assignment account selection

Status: implementation candidate for Helivanta Staff access preparation; not a
live rollout or completed Staff onboarding claim.

Hospital governance reviewers must select the intended existing account by its
current native name and verified address. A subject ID alone is unsuitable for
normal selection; names alone cannot distinguish different people. Current-user
`profile` reads in ADR-0005 remain current-user reads and retain their existing
legacy behavior.

Add the private `assignment_candidate` purpose to the existing workload-only
identity projection contract. It returns a separate optional `candidate` containing
only display name and verified email. It never returns the self-profile or tenant
name. It requires a recognized active human, verified valid email, an exact native
resource owner matching this active/enrolled Helivanta customer, and a valid bounded
native display name. Missing/ineligible candidates return no candidate; native
refusal, malformed/ambiguous data, missing dependencies and audit failure are
unavailable. No fallback display name, native home-org inference, external account
adoption, role, membership or operating authority is created.

The verified product workload is responsible for checking the caller's current
hospital assignment-management authority and target IAM membership before asking
for this purpose, and for rechecking both before browser disclosure after remote
I/O. The identity owner validates its own customer/product/native organization
scope before the native read and again before releasing this selection projection.
Every successful projection retains the existing subject/purpose/request read
audit before release; names and addresses stay out of audit detail and errors.
There is no identity master, profile cache, new public identity endpoint or
expansion of Helivanta's native login credential.

The identity owner performs one bounded native subject read per request, within
its existing ten-second request deadline and five-second provider deadline. The
consumer must use a bounded candidate page, bounded concurrency and its shorter
request deadline; availability/latency or growth targets beyond the existing
service have not been measured or asserted. A provider failure cannot become an
empty successful choice list. No remote call is held in a database transaction.

The additional optional wire field appears only for the new purpose. Existing
status, membership and self-profile shapes remain unchanged for strict older
consumers. Generate the Go-owned Zod contract, deploy this owner before opting in
consumers, and pin the consumer's contract dependency to the accepted source.
Consumer rollback stops requesting the new purpose; no schema migration or data
rollback is needed. Cost is the existing identity read plus a second customer-scope
revalidation and the retained audit; no service or datastore is introduced.

Acceptance needs strict contract tests, owned/foreign/unverified/inactive/machine
and malformed native cases, audit-before-disclosure and enrollment-revoked-during-
read cases against the real owner service, then actual consumer integration and
allowed/denied/revoked hospital selection. Pure unit tests do not close the
service, consumer, deployment or browser gates.
