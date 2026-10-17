# Adopt an existing tenant through the onboarding authority

Related: #12; [ADR0003](../adr/0003-existing-organisation-enrolment.md).
This endpoint registers an existing binding; it does not provision replacement
Zitadel organisations, credentials, users or permissions to trade.

`POST /v1/orgs/enrolments` requires a platform administrator. Browser sessions
must use the existing CSRF header. Product machines, customer administrators and
read-only platform operators cannot invoke it. Use the normal authenticated
console/API flow; never copy a console cookie into a script.

```json
{
  "tenantId": "11111111-1111-4111-8111-111111111111",
  "zitadelOrgId": "EXISTING_SOURCE_ORGANISATION_ID",
  "productKey": "REGISTERED_PRODUCT_KEY",
  "name": "TEST Existing Pharmacy",
  "reason": "Enrol the existing synthetic tenant while preserving its identity"
}
```

These values are illustrative. Select the exact existing UUID from verified
Zitadel `tenant_id` metadata; do not generate a new UUID. The owner service
verifies the supplied binding against live source facts and the exact active
registered product project. The name is an administrative display label only.
Do not enter production medical, banking or private contact data in the reason.

| Result | Meaning |
| --- | --- |
| 200 | Existing binding is enrolled; response uses the existing organisation contract and unchanged tenant UUID. Repeating it does not duplicate the grant or audit. |
| 400 | Missing or invalid claims; correct the request. |
| 401 / 403 | Normal authentication / platform administrator permission is required. |
| 404 | Product or live source identity/grant is missing. |
| 409 | Inactive source, conflicting UUID/source binding, inactive registry organisation, revoked grant, or product configuration changed during verification. Resolve through the owning governance workflow. |
| 503 | Live source or registry/audit transaction is unavailable. No fallback identity or approval is inferred. Retry after recovery. |

The operation never overwrites an existing profile, revives revoked access,
infers user roles or approves licences. Fresh product-machine identity requests
remain the authority for current eligibility after enrolment. Success audit
records include actor, request, reason and live source revisions in the same
transaction as the registry change. No SQL backfill is needed.

Before enabling Helivanta, separately register its actual product/project,
provision a dedicated least-privilege product machine, configure the actual
onboarding token audience, and deliver its credential through OpenBao/ESO.
Verify current and revoked identities with the real product machine. Neither a
healthy workload nor a successful administrator enrolment proves this final
machine integration works.
