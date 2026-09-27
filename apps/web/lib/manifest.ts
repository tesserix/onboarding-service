const ISSUER = "https://auth.tesserix.app";

export type ManifestInput = {
  key: string;
  displayName: string;
  hosts: string[];
  identity: "zitadel" | "gip";
  tenantSecrets: "openbao" | "none";
  roles: string[];
};

export type Snippet = { title: string; path: string; body: string };

const list = (items: string[], indent: string) =>
  items.length ? items.map((item) => `${indent}- ${item}`).join("\n") : `${indent}[]`;

// The API creates the live objects; these are their git counterparts, so a
// rebuilt cluster reproduces the product instead of inheriting it.
export function productManifests(input: ManifestInput): Snippet[] {
  if (input.key.length > 63 || !/^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/.test(input.key)) {
    throw new Error("Product key must be a Kubernetes namespace label");
  }
  const snippets: Snippet[] = [];
  const redirects = input.hosts.map((host) => `https://${host}/auth/callback`);

  if (input.identity === "zitadel") {
    snippets.push({
      title: "Zitadel project",
      path: "charts/thirdparty/zitadel-bootstrap/values.yaml",
      body: `desired:
  projects:
    - name: ${input.key}
      displayName: ${JSON.stringify(input.displayName)}
      roles:
${list(input.roles, "        ")}
      applications:
        - name: ${input.key}-web
          type: oidc
          grantTypes: [authorization_code]
          pkce: true
          redirectUris:
${list(redirects, "            ")}
        - name: ${input.key}-api
          type: api
          authMethod: private_key_jwt`,
    });
  }

  snippets.push({
    title: "Application secrets",
    path: `external-secrets/prod/${input.key}/onboarding.yaml`,
    body: `apiVersion: v1
kind: ServiceAccount
metadata:
  name: ${input.key}-identity-reader
  namespace: ${input.key}
automountServiceAccountToken: false
---
apiVersion: external-secrets.io/v1
kind: SecretStore
metadata:
  name: openbao-${input.key}-identity
  namespace: ${input.key}
spec:
  provider:
    vault:
      server: http://openbao.openbao.svc.cluster.local:8200
      path: kv
      version: v2
      auth:
        kubernetes:
          mountPath: kubernetes
          role: read-${input.key}-identity
          serviceAccountRef:
            name: ${input.key}-identity-reader
---
apiVersion: external-secrets.io/v1
kind: ExternalSecret
metadata:
  name: ${input.key}-identity
  namespace: ${input.key}
spec:
  refreshInterval: 1h
  secretStoreRef:
    name: openbao-${input.key}-identity
    kind: SecretStore
  target:
    name: ${input.key}-identity
  data:
    - secretKey: OIDC_CLIENT_SECRET
      remoteRef:
        key: ${input.key}/app/${input.key}-oidc-client-secret
        property: value
    - secretKey: MACHINE_KEY
      remoteRef:
        key: ${input.key}/app/${input.key}-machine-key
        property: value`,
  });

  snippets.push({
    title: "OpenBao application access",
    path: "charts/thirdparty/openbao/values.yaml",
    body: `# Merge these entries into bootstrap; keep existing platform policies.
# Provision field value using a temporary exact-path writer, then revoke it.
bootstrap:
  policies:
    - name: read-${input.key}-identity
      hcl: |
        path "kv/data/${input.key}/app/${input.key}-oidc-client-secret" { capabilities = ["read"] }
        path "kv/data/${input.key}/app/${input.key}-machine-key" { capabilities = ["read"] }
  kubernetesRoles:
    - name: read-${input.key}-identity
      serviceAccounts: ["${input.key}-identity-reader"]
      namespaces: ["${input.key}"]
      policies: ["read-${input.key}-identity"]
      ttl: 15m`,
  });

  snippets.push({
    title: "Mesh authorization",
    path: `charts/apps/${input.key}/templates/authorization-policy.yaml`,
    body: `apiVersion: security.istio.io/v1
kind: RequestAuthentication
metadata:
  name: ${input.key}
  namespace: ${input.key}
spec:
  selector:
    matchLabels:
      app.kubernetes.io/name: ${input.key}
  jwtRules:
    - issuer: ${ISSUER}
      jwksUri: ${ISSUER}/oauth/v2/keys
      audiences:
        - ${input.key}`,
  });

  if (input.tenantSecrets === "openbao") {
    snippets.push({
      title: "OpenBao tenant path",
      path: `charts/apps/openbao/policies/${input.key}.yaml`,
      body: `# Tenant credentials have separate scope from application readers.
# GCP retains only critical platform/bootstrap/recovery secrets.
policy: |
  path "kv/data/${input.key}/tenants/{{identity.entity.aliases.auth_jwt.metadata.tenant_id}}/*" {
    capabilities = ["create", "read", "update", "delete", "list"]
  }`,
    });
  }

  return snippets;
}
