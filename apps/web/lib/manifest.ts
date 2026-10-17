import type { Product } from "./types";

export type ManifestInput = Pick<
  Product,
  | "key"
  | "displayName"
  | "hosts"
  | "identity"
  | "tenantSecrets"
  | "roles"
  | "zitadelProjectId"
  | "oidcClientId"
> & { machineUserId?: string };
export type Snippet = { title: string; path: string; body: string };

export function productManifests(input: ManifestInput): Snippet[] {
  const review = [
    "Registration records metadata; it does not deploy or change identity resources.",
    input.identity === "zitadel"
      ? "Review the existing project, application and machine claims in the Zitadel operator GitOps configuration. Preserve their live IDs, token contracts, callbacks and login policy."
      : "Review the existing identity provider configuration and token contract in its owning repository.",
    "Copy exact verified callback URLs from the application configuration; a host alone does not determine a callback path.",
    "Verify the intended token type and numeric resource audience on each API. Authentication at the mesh does not replace application membership and resource authorization.",
    "Store application and operational credentials in environment-scoped OpenBao paths with product-prefixed identifiers, namespace-bound identities and exact-path policies. Review writers, readers and recovery together.",
    ...(input.tenantSecrets === "openbao"
      ? [
          "Customer secret access requires a verified tenant binding and exact environment/tenant paths; do not grant a product-wide browser policy or derive access from an unverified claim.",
        ]
      : []),
    ...(input.key === "helivanta"
      ? [
          "Use the private hospital onboarding workflow for customer identities. A group may share an org/tenant; independent entities need separate boundaries and shared staff need explicit memberships.",
        ]
      : []),
  ];
  return [
    {
      title: "Product integration review",
      path: `docs/onboarding/${input.key}-integration.yaml`,
      body:
        "# Review record only; this is not a Kubernetes deployment manifest.\n" +
        JSON.stringify(
          {
            registry: {
              key: input.key,
              displayName: input.displayName,
              hosts: input.hosts,
              identity: input.identity,
              tenantSecrets: input.tenantSecrets,
              roles: input.roles,
              zitadelProjectId: input.zitadelProjectId,
              machineUserId: input.machineUserId,
              oidcClientId: input.oidcClientId,
            },
            review,
          },
          null,
          2,
        ),
    },
  ];
}
