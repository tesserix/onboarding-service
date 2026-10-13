import { renderToStaticMarkup } from "react-dom/server";
import { expect, it, vi } from "vitest";
import { TopBar } from "./TopBar";

vi.mock("next/navigation", () => ({ usePathname: () => "/orgs/example" }));

it("marks Organizations as the current section and exposes labeled navigation", () => {
  const html = renderToStaticMarkup(<TopBar />);
  const doc = new DOMParser().parseFromString(html, "text/html");
  const navigation = doc.querySelector('nav[aria-label="Main navigation"]');
  expect(navigation).not.toBeNull();
  expect(navigation?.querySelector('a[href="/orgs"]')?.getAttribute("aria-current")).toBe("page");
  expect(navigation?.querySelectorAll('[aria-current="page"]').length).toBe(1);
  expect(doc.querySelector('form[action="/api/auth/logout"]')?.getAttribute("method")).toBe("post");
});
