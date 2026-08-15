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
    title: "Platform secrets",
    path: `external-secrets/prod/${input.key}/onboarding.yaml`,
    body: `apiVersion: external-secrets.io/v1
kind: ExternalSecret
metadata:
  name: ${input.key}-identity
  namespace: ${input.key}
spec:
  refreshInterval: 1h
  secretStoreRef:
    name: gcp-secret-store
    kind: ClusterSecretStore
  target:
    name: ${input.key}-identity
  data:
    - secretKey: OIDC_CLIENT_SECRET
      remoteRef:
        key: prod-${input.key}-oidc-client-secret
    - secretKey: MACHINE_KEY
      remoteRef:
        key: prod-${input.key}-machine-key`,
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
      body: `# Customer secrets only. Platform credentials stay in GCP Secret Manager.
policy: |
  path "kv/data/${input.key}/tenants/{{identity.entity.aliases.auth_jwt.metadata.tenant_id}}/*" {
    capabilities = ["create", "read", "update", "delete", "list"]
  }`,
    });
  }

  return snippets;
}
