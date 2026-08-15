import { afterEach, describe, expect, it, vi } from "vitest";

import { ApiError, apiFetch, csrfToken } from "./api";

function stubCookie(value: string) {
  Object.defineProperty(document, "cookie", { value, writable: true, configurable: true });
}

function stubFetch(response: Response) {
  const fetchMock = vi.fn().mockResolvedValue(response);
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": status >= 400 ? "application/problem+json" : "application/json" },
  });
}

afterEach(() => {
  vi.unstubAllGlobals();
  stubCookie("");
});

describe("csrfToken", () => {
  it("reads the companion cookie the API sets alongside the session", () => {
    stubCookie("other=1; onboard_csrf=tok-123; another=2");
    expect(csrfToken()).toBe("tok-123");
  });

  it("is empty when the cookie is absent", () => {
    stubCookie("other=1");
    expect(csrfToken()).toBe("");
  });
});

describe("apiFetch", () => {
  it("sends cookies and no CSRF header on a read", async () => {
    stubCookie("onboard_csrf=tok-123");
    const fetchMock = stubFetch(json({ items: [] }));

    await apiFetch("/v1/orgs");

    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe("/v1/orgs");
    expect(init.credentials).toBe("same-origin");
    expect(init.headers["X-CSRF-Token"]).toBeUndefined();
  });

  it("sends the CSRF token on a write", async () => {
    stubCookie("onboard_csrf=tok-123");
    const fetchMock = stubFetch(json({ id: "o1" }, 201));

    await apiFetch("/v1/orgs", { method: "POST", body: { name: "Acme" } });

    const [, init] = fetchMock.mock.calls[0];
    expect(init.method).toBe("POST");
    expect(init.headers["X-CSRF-Token"]).toBe("tok-123");
    expect(init.body).toBe(JSON.stringify({ name: "Acme" }));
  });

  it("sends an idempotency key on a write so a retry cannot double-create", async () => {
    const fetchMock = stubFetch(json({}, 201));

    await apiFetch("/v1/orgs", { method: "POST", body: {}, idempotencyKey: "key-1" });

    expect(fetchMock.mock.calls[0][1].headers["Idempotency-Key"]).toBe("key-1");
  });

  it("surfaces the problem document detail", async () => {
    stubFetch(json({ title: "Bad Request", detail: "name is required" }, 400));

    await expect(apiFetch("/v1/orgs", { method: "POST", body: {} })).rejects.toThrow(
      "name is required",
    );
  });

  it("reports the status so a caller can tell 404 from 500", async () => {
    stubFetch(json({ detail: "not found" }, 404));

    const err = await apiFetch("/v1/orgs/x").catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).status).toBe(404);
  });

  it("sends an expired session back to sign-in rather than showing an error", async () => {
    stubFetch(json({ detail: "authentication required" }, 401));
    const redirect = vi.fn();
    vi.stubGlobal("location", { assign: redirect, pathname: "/orgs" } as unknown as Location);

    await expect(apiFetch("/v1/orgs")).rejects.toThrow();
    expect(redirect).toHaveBeenCalledWith("/api/auth/login");
  });

  it("returns null for a 204 rather than failing to parse an empty body", async () => {
    stubFetch(new Response(null, { status: 204 }));

    await expect(apiFetch("/v1/orgs/o1/grants/pp", { method: "DELETE" })).resolves.toBeNull();
  });
});
