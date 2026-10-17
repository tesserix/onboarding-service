import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { HospitalOnboarding } from "./HospitalOnboarding";

let container: HTMLDivElement;
let root: Root;
let admin = true;
let failStart = false;
let complete = false;
let wrongTenant = false;
const tenant = "11111111-1111-4111-8111-111111111111";
const requests: Array<{ path: string; body: Record<string, unknown>; key: string | null }> = [];
const spec = {
  product: "helivanta",
  tenantId: tenant,
  displayName: "TEST Hospital Group",
  initialAdminUserId: "verified-admin",
  organizationId: "existing-org",
  preserveLoginPolicy: true,
};

beforeEach(() => {
  admin = true;
  failStart = false;
  complete = false;
  wrongTenant = false;
  requests.length = 0;
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, options?: RequestInit) => {
      if (path === "/v1/me")
        return Response.json({
          kind: "platform",
          roles: [admin ? "platform-admin" : "platform-operator"],
        });
      if (options?.method === "POST") {
        requests.push({
          path,
          body: JSON.parse(options.body as string),
          key: new Headers(options.headers).get("Idempotency-Key"),
        });
        if (failStart) return Response.json({ detail: "temporarily unavailable" }, { status: 503 });
        return Response.json({ spec, status: {} }, { status: 202 });
      }
      return Response.json({
        state: complete ? "complete" : "pending",
        onboarding: {
          spec: { ...spec, tenantId: wrongTenant ? "foreign" : tenant },
          status: {
            archiveObject: "helivanta/prod/tenants/test/identity.yaml",
            archiveGeneration: "123",
            conditions: [
              { type: "Ready", reason: "Reconciling", message: "Waiting on identity activation" },
            ],
          },
        },
      });
    }),
  );
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
});
afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
  vi.unstubAllGlobals();
});
async function submit() {
  await act(async () =>
    container
      .querySelector("form")!
      .dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })),
  );
}
async function adoption() {
  await act(async () => root.render(<HospitalOnboarding />));
  const mode = container.querySelector<HTMLSelectElement>('[name="mode"]')!;
  await act(async () => {
    mode.value = "adopt";
    mode.dispatchEvent(new Event("change", { bubbles: true }));
  });
  for (const [name, value] of Object.entries({
    displayName: spec.displayName,
    initialAdminUserId: spec.initialAdminUserId,
    tenantId: tenant,
    organizationId: spec.organizationId,
  }))
    container.querySelector<HTMLInputElement>(`[name="${name}"]`)!.value = value;
}
it("adopts without changing IDs and reports completion only after a current controller receipt", async () => {
  await adoption();
  await submit();
  expect(requests[0].path).toBe("/v1/hospital-onboardings");
  expect(requests[0].body).toEqual(spec);
  expect(container.textContent).toContain("Onboarding pending");
  expect(container.textContent).not.toContain("Onboarding complete");
  complete = true;
  await act(async () =>
    container.querySelector<HTMLButtonElement>('[data-action="check-status"]')!.click(),
  );
  expect(container.textContent).toContain("Onboarding complete");
  expect(container.textContent).toContain("123");
  expect(container.textContent).toContain(tenant);
});
it("reuses the request key after an uncertain response", async () => {
  await adoption();
  failStart = true;
  await submit();
  expect(container.textContent).toContain("temporarily unavailable");
  failStart = false;
  await submit();
  expect(requests).toHaveLength(2);
  expect(requests[0].key).toBeTruthy();
  expect(requests[1].key).toBe(requests[0].key);
});
it("does not offer writes to a platform operator", async () => {
  admin = false;
  await act(async () => root.render(<HospitalOnboarding />));
  expect(container.querySelector("form")).toBeNull();
  expect(requests).toHaveLength(0);
});
it("rejects a status response for a different tenant", async () => {
  await adoption();
  wrongTenant = true;
  complete = true;
  await submit();
  expect(container.querySelector('[role="alert"]')?.textContent).toContain("did not match");
  expect(container.textContent).not.toContain("Onboarding complete");
});
