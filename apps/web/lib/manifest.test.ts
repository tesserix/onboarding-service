import { describe, expect, it } from "vitest";
import { productManifests, type ManifestInput } from "./manifest";

const base = {
  key: "planning-poker",
  displayName: "Planning Poker",
  hosts: ["poker.tesserix.app"],
  identity: "zitadel" as const,
  tenantSecrets: "openbao" as const,
  roles: ["poker-user", "poker-admin"],
  zitadelProjectId: "existing-project",
  machineUserId: "existing-machine",
  oidcClientId: "existing-client",
};
const reviewRecord = (input: ManifestInput = base) =>
  JSON.parse(productManifests(input)[0].body.split("\n").slice(1).join("\n"));

describe("productManifests", () => {
  it("preserves registered IDs and configuration in a review record without deploying identity", () => {
    const snippets = productManifests(base);
    expect(snippets).toHaveLength(1);
    expect(snippets[0].path).toBe("docs/onboarding/planning-poker-integration.yaml");
    expect(reviewRecord().registry).toEqual(base);
    expect(reviewRecord().review.join(" ")).toContain("does not deploy");
    expect(snippets[0].body).not.toContain("apiVersion:");
  });
  it("never guesses identity contracts or proposes broad secret access", () => {
    const output = productManifests(base)
      .map((s) => s.body)
      .join("\n");
    for (const unsafe of [
      "gcp-secret-store",
      "GCP Secret Manager",
      "zitadel-bootstrap",
      "/auth/callback",
      '"delete"',
      "kind: RequestAuthentication",
    ])
      expect(output).not.toContain(unsafe);
    expect(output).toContain("OpenBao");
    expect(output).toContain("exact-path");
    expect(output).toContain("numeric resource audience");
  });
  it("adds customer secret review only for products holding customer secrets", () => {
    expect(reviewRecord().review.join(" ")).toContain("Customer secret access");
    const without = reviewRecord({ ...base, tenantSecrets: "none" });
    expect(without.review.join(" ")).not.toContain("Customer secret access");
    expect(without.review.join(" ")).toContain("operational credentials");
  });
  it("does not prescribe Zitadel resources for a GIP product", () => {
    const record = reviewRecord({ ...base, identity: "gip" });
    expect(record.review.join(" ")).not.toContain("Zitadel operator");
    expect(record.registry.identity).toBe("gip");
  });
  it("keeps display names as literal data instead of YAML or policy injection", () => {
    const displayName = 'Hospital\napiVersion: rbac.authorization.k8s.io/v1\n"';
    expect(reviewRecord({ ...base, displayName }).registry.displayName).toBe(displayName);
  });
  it("directs Helivanta to the private hospital workflow and explicit memberships", () => {
    const record = reviewRecord({ ...base, key: "helivanta" });
    expect(record.review.join(" ")).toContain("private hospital onboarding");
    expect(record.review.join(" ")).toContain("explicit memberships");
  });
});
