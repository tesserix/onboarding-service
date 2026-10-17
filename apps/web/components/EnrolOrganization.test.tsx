import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { EnrolOrganization } from "./EnrolOrganization";

let container: HTMLDivElement;
let root: Root;
const tenant = "11111111-1111-4111-8111-111111111111";
const enrolments: Array<{ path: string; body: Record<string, string>; headers: HeadersInit }> = [];
let submit: (response: Response) => void;
let isAdmin = true;

beforeEach(() => {
  enrolments.length = 0;
  isAdmin = true;
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, options?: RequestInit) => {
      if (path === "/v1/me")
        return Response.json({
          kind: "platform",
          roles: [isAdmin ? "platform-admin" : "platform-operator"],
        });
      if (path === "/v1/products")
        return Response.json({
          products: [
            { key: "helivanta", displayName: "Helivanta", identity: "zitadel", state: "active" },
            {
              key: "planning-poker",
              displayName: "Planning Poker",
              identity: "zitadel",
              state: "active",
            },
          ],
        });
      enrolments.push({
        path,
        body: JSON.parse(options!.body as string),
        headers: options!.headers!,
      });
      return new Promise<Response>((resolve) => {
        submit = resolve;
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

async function renderAndSubmit() {
  await act(async () => root.render(<EnrolOrganization />));
  for (const [name, value] of Object.entries({
    tenantId: tenant,
    zitadelOrgId: "existing-org",
    productKey: "planning-poker",
    name: "TEST Existing Pharmacy",
    reason: "Preserve existing synthetic identity",
  })) {
    const input = container.querySelector<
      HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement
    >(`[name="${name}"]`)!;
    input.value = value;
  }
  await act(async () =>
    container
      .querySelector("form")!
      .dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })),
  );
}

it("submits the existing binding and displays its unchanged UUID only after success", async () => {
  await renderAndSubmit();
  expect(enrolments).toHaveLength(1);
  expect(enrolments[0].path).toBe("/v1/orgs/enrolments");
  expect(enrolments[0].body).toEqual({
    tenantId: tenant,
    zitadelOrgId: "existing-org",
    productKey: "planning-poker",
    name: "TEST Existing Pharmacy",
    reason: "Preserve existing synthetic identity",
  });
  expect(new Headers(enrolments[0].headers).get("Idempotency-Key")).toBeTruthy();
  expect(container.textContent).not.toContain("Organisation enrolled");
  expect(container.querySelector<HTMLFieldSetElement>("fieldset")!.disabled).toBe(true);
  await act(async () =>
    submit(Response.json({ id: tenant, zitadelOrgId: "existing-org", state: "active" })),
  );
  expect(container.querySelector('[role="status"]')!.textContent).toContain(
    "Organisation enrolled",
  );
  expect(container.querySelector(`a[href="/orgs/${tenant}"]`)).not.toBeNull();
  expect(container.textContent).toContain("Trading approval is separate");
});

it("shows an actionable error and preserves the form after a rejected source check", async () => {
  await renderAndSubmit();
  await act(async () =>
    submit(
      Response.json({ detail: "a dependency is unavailable; no change was made" }, { status: 503 }),
    ),
  );
  expect(container.querySelector('[role="alert"]')!.textContent).toContain(
    "dependency is unavailable",
  );
  expect(container.textContent).not.toContain("Organisation enrolled");
  expect(container.querySelector<HTMLInputElement>('[name="tenantId"]')!.value).toBe(tenant);
  expect(container.querySelector<HTMLFieldSetElement>("fieldset")!.disabled).toBe(false);
});

it("does not offer enrolment to a read-only platform operator", async () => {
  isAdmin = false;
  await act(async () => root.render(<EnrolOrganization />));
  expect(container.querySelector("form")).toBeNull();
  expect(container.textContent).toContain("platform administrator");
  expect(enrolments).toHaveLength(0);
});

it("does not accept a response for a different tenant", async () => {
  await renderAndSubmit();
  await act(async () =>
    submit(
      Response.json({
        id: "22222222-2222-4222-8222-222222222222",
        zitadelOrgId: "existing-org",
        state: "active",
      }),
    ),
  );
  expect(container.querySelector('[role="alert"]')).not.toBeNull();
  expect(container.textContent).not.toContain("Organisation enrolled");
});

it("routes Helivanta adoption through the retained hospital workflow", async () => {
  await act(async () => root.render(<EnrolOrganization />));
  expect(container.querySelector('option[value="helivanta"]')).toBeNull();
  expect(container.querySelector('a[href="/orgs/hospital"]')).not.toBeNull();
});
