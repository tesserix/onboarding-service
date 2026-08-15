import { describe, expect, it } from "vitest";

import { productManifests } from "./manifest";

const base = {
  key: "planning-poker",
  displayName: "Planning Poker",
  hosts: ["poker.tesserix.app"],
  identity: "zitadel" as const,
  tenantSecrets: "openbao" as const,
  roles: ["poker-user", "poker-admin"],
};

describe("productManifests", () => {
  it("names a file path for every snippet so the operator knows where it goes", () => {
    for (const snippet of productManifests(base)) {
      expect(snippet.path).toMatch(/^[a-z0-9._/-]+\.ya?ml$/);
      expect(snippet.body.trim()).not.toBe("");
    }
  });

  it("puts the project, its roles and its hosts in the bootstrap entry", () => {
    const bootstrap = productManifests(base).find((s) => s.path.includes("zitadel-bootstrap"));
    expect(bootstrap?.body).toContain("- name: planning-poker");
    expect(bootstrap?.body).toContain("- poker-admin");
    expect(bootstrap?.body).toContain("https://poker.tesserix.app/auth/callback");
  });

  it("reads both product secrets from GCP Secret Manager under the prod- prefix", () => {
    const secret = productManifests(base).find((s) => s.path.startsWith("external-secrets/"));
    expect(secret?.body).toContain("prod-planning-poker-oidc-client-secret");
    expect(secret?.body).toContain("prod-planning-poker-machine-key");
    expect(secret?.body).toContain("gcp-secret-store");
  });

  it("pins the authorization policy to the Zitadel issuer and the product audience", () => {
    const policy = productManifests(base).find((s) => s.path.includes("authorization-policy"));
    expect(policy?.body).toContain("https://auth.tesserix.app");
    expect(policy?.body).toContain("planning-poker");
  });

  it("emits an OpenBao policy only when the product holds customer secrets", () => {
    const withBao = productManifests(base).map((s) => s.path);
    expect(withBao.some((p) => p.includes("openbao"))).toBe(true);

    const without = productManifests({ ...base, tenantSecrets: "none" }).map((s) => s.path);
    expect(without.some((p) => p.includes("openbao"))).toBe(false);
  });

  it("does not emit Zitadel objects for a GIP product", () => {
    const paths = productManifests({ ...base, identity: "gip" }).map((s) => s.path);
    expect(paths.some((p) => p.includes("zitadel-bootstrap"))).toBe(false);
  });
});
