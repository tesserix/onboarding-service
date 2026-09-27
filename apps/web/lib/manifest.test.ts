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

  it("reads both application secrets from a namespaced OpenBao store", () => {
    const secret = productManifests(base).find((s) => s.path.startsWith("external-secrets/"));
    expect(secret?.body).toContain("planning-poker/app/planning-poker-oidc-client-secret");
    expect(secret?.body).toContain("planning-poker/app/planning-poker-machine-key");
    expect(secret?.body).toContain("openbao-planning-poker-identity");
    expect(secret?.body).toContain("property: value");
    expect(secret?.body).not.toContain("gcp-secret-store");
  });

  it("pins the authorization policy to the Zitadel issuer and the product audience", () => {
    const policy = productManifests(base).find((s) => s.path.includes("authorization-policy"));
    expect(policy?.body).toContain("https://auth.tesserix.app");
    expect(policy?.body).toContain("planning-poker");
  });

  it("emits tenant access only for products holding customer secrets", () => {
    const withBao = productManifests(base).map((s) => s.title);
    expect(withBao.some((p) => p === "OpenBao tenant path")).toBe(true);

    const without = productManifests({ ...base, tenantSecrets: "none" }).map((s) => s.title);
    expect(without.some((p) => p === "OpenBao tenant path")).toBe(false);
  });

  it("does not emit Zitadel objects for a GIP product", () => {
    const paths = productManifests({ ...base, identity: "gip" }).map((s) => s.path);
    expect(paths.some((p) => p.includes("zitadel-bootstrap"))).toBe(false);
  });
});


describe("application access scope", () => {
  it("still provisions app readers when tenant secrets are disabled", () => {
    const snippets = productManifests({ ...base, tenantSecrets: "none" });
    const access = snippets.find((s) => s.title === "OpenBao application access");
    expect(access?.body).toContain('path "kv/data/planning-poker/app/planning-poker-oidc-client-secret"');
    expect(access?.body).toContain('capabilities = ["read"]');
    expect(access?.body).toContain('serviceAccounts: ["planning-poker-identity-reader"]');
    expect(access?.body).toContain('namespaces: ["planning-poker"]');
    expect(access?.body).not.toContain('"create"');
    expect(access?.body).not.toContain('*');
  });
  it.each(["../other", "bad\nnamespace", "", "UPPER", "x".repeat(64)])("rejects unsafe product key %s", (key) => {
    expect(() => productManifests({ ...base, key })).toThrow();
  });
});
