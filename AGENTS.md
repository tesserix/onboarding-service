# Application-secret storage

Generated product manifests must use OpenBao for application credentials,
including OIDC client secrets, machine keys, database and provider credentials.
Use `<product>/app/<product>-<secret-name>` paths and exact read-only access bound
to the product namespace and ServiceAccount. Tenant credentials use separate
scoped paths. A product with no tenant secrets still uses OpenBao for app secrets.

GCP Secret Manager is reserved for critical platform/bootstrap/recovery secrets,
including the onboarding service's platform IAM bootstrap identity where needed.
Do not label ordinary product credentials as platform secrets to bypass this rule.
See `tesserix-k8s/docs/application-secret-policy.md` for migration and recovery gates.
