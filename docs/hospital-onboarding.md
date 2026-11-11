# Hospital and group onboarding

The trusted platform console or Helivanta backend calls
`POST /v1/hospital-onboardings` on `https://console.tesserix.app` with an
`Idempotency-Key` of 16–200 characters. Console sessions retain the normal CSRF
check. Service tokens must be issued for the configured onboarding project
audience and map to the registered Helivanta machine user. Browser users never
receive that service credential.

```json
{
  "product": "helivanta",
  "displayName": "Example Hospital Group",
  "initialAdminUserId": "existing-verified-human-subject"
}
```

For a first customer without an existing identity, the same trusted API accepts
an `initialAdmin` profile instead of `initialAdminUserId`:

```json
{
  "product": "helivanta",
  "displayName": "Example Hospital Group",
  "initialAdmin": {
    "givenName": "Example",
    "familyName": "Owner",
    "email": "owner@example.test"
  }
}
```

The API derives `initialAdminUserId` as `owner-<tenantId>`. Before writing a new
profile-bearing claim, a server-side dry run verifies that the installed schema
preserves the complete binding. Schema pruning fails without writing an
incomplete immutable claim. The real creation response is checked as well. Sending both owner
modes, an invalid profile or a new-owner profile with adoption fields is rejected.
The profile uses the operator's canonical Go type, pinned by commit from the
public `tesserix/tesserix-operators` repository. Its legacy Go module path is
resolved with an explicit versioned module replacement; there is no local path
replacement or independently copied claim model.

Zitadel creates the organization and its human owner together. Its native
verification email is requested without a password, returned code or fabricated
verification. The controller reports `OwnerVerificationPending` until that exact
active human has the expected home organization and verified email; it grants no
product access while waiting. Polling does not recreate the owner or resend a
code. A changed-profile retry conflicts. Profiles remain in private runtime
claims, never public GitOps or logs; cleartext names/email are removed from held
archive payloads and replaced by an immutable spec fingerprint. Restoring a
profile-bearing claim requires its authorized profile source as well as the
archive fingerprint.

This is a trusted provisioning capability. The public self-service signup
journey, automatic hospital/pharmacy setup and owner security panel are separate
integration gates and are not implemented by this endpoint. The intended public
policy is automatic onboarding after verified signup, with the first registrant
becoming the tenant owner; platform approval is not required. Identity completion
does not certify a pharmacy licence, clinical authority or financial authority.

The product backend must derive the initial subject from its authenticated
user/workflow, rather than copying an untrusted browser's ownership assertion.
The service derives a stable new tenant UUID from the authenticated caller and
idempotency key. A service caller cannot select an existing org or tenant.
Requests for another product return 404. An ordinary customer session cannot
provision, adopt, or manage identity organizations.

A platform administrator may additionally provide `tenantId` and
`organizationId` to adopt an existing binding. The controller requires the
live org's tenant metadata to match. `preserveLoginPolicy: true` is allowed only
for explicit adoption during a staged MFA cutover; it does not assert MFA
compliance. Use the existing clinical administrator's subject when adopting;
the product refuses to add a new administrator to an existing tenant.

The 202 response contains the private claim's spec and current status. Poll
`GET /v1/hospital-onboardings/{tenantId}?product=helivanta`; only
`state: complete` means identity, product membership and archive have finished.
A timeout, source outage, OpenFGA outage or archive failure leaves pending and
is safe to retry. Replaying a different request with the same key conflicts.
Customer claims never enter the public GitOps repository. The runtime API has
create/get permission only for organization claims, not delete, patch, status,
project, application or machine resources.

The internal controller endpoint is authenticated separately with a Kubernetes
TokenReview for audience `onboarding-provisioning` and exact account
`system:serviceaccount:identity-operator:zitadel-operator`. It checks the stored
claim and a fresh live Zitadel proof, enrolls the projection/audit transaction,
then calls Helivanta with a distinct 10-minute `helivanta-provisioning` token.
Neither workload receives the other's token or a clinical user's session.
Completed claims carry a persistent activation marker: drift checks repair
existing authorization tuples but cannot recreate revoked final membership.

An organization is an identity-administration boundary, not a building. A group
may contain several hospitals, branches, pharmacies and labs in one tenant
where governance is shared. Independent entities use separate organizations
and tenants. One human keeps a stable Zitadel subject with explicit memberships
in multiple tenants and explicit hospital/resource permissions. An org grant,
a shared email domain, group ownership or a supply relationship never grants
patient or staff access on its own.

GCS revisions are under
`helivanta/prod/tenants/{tenantId}/revisions/{specSha256}/intent.yaml` and
`identity.yaml` in the configured private archive bucket. The status records the
completion object's GCS generation. Revisions use conditional creates, holds,
versioning and no lifecycle expiration. Secrets/clinical payloads are excluded;
service credentials are stored in OpenBao. Privileged administrators can still
change retention/IAM, which requires separate organizational governance.

## Platform console

Platform administrators can use `/orgs/hospital` to create a new hospital or adopt an existing one. Adoption requires the exact existing tenant UUID, Zitadel organization ID and existing Helivanta administrator subject. Preserve login policy is selected initially to avoid an unplanned MFA/domain cutover. Keep the resume link to check pending work after navigation. The console displays completion only after the server verifies activation and a retained receipt. A retry with the same unchanged form retains its idempotency key; after an uncertain response, retry in that form rather than starting a second new hospital request.

Legacy organization creation, product grants and generic enrolment reject Helivanta. They remain available for other products. Product registration records reviewed metadata only and does not deploy infrastructure.

## Existing Helivanta customer accounts

The public receipt remains generic when a submitted owner email already exists.
An existing account continues through a fresh native OIDC sign-in. The Helivanta
workload calls `POST /internal/v1/customer-signups/:id/existing-owner` with the
receipt hash and `ExistingOwnerInput` containing the verified subject,
authentication time and next receipt hash. The workload verifies the ID token
before this handoff. The onboarding owner independently verifies the current
active native human and verified email against the immutable submitted intent.

The database records an immutable existing-owner binding, rotates the receipt
and resumes the original tenant/resource command. Reauthentication by the same
bound subject can recover a lost response. Another subject cannot replace the
binding. New-account email uniqueness remains enforced while one verified
existing person can own multiple separate workspaces. Native tokens are never
persisted in this ledger. The controller uses its existing-user organization
membership path. Business initialization still checks current organization
metadata, project grant and native ORG_OWNER membership.

Deploy the reviewed existing-owner columns and guards in `onboarding_db.sql`
before deploying the API version that reads them. The CI schema pin includes
that change and exercises the real PostgreSQL continuation and replay tests.
