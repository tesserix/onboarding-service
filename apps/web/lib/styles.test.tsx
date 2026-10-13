// @vitest-environment node
import { readFile } from "node:fs/promises";
import { createRequire } from "node:module";
import { resolve } from "node:path";
import { expect, it, vi } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import tailwind from "@tailwindcss/postcss";

vi.mock("@/components/TopBar", () => ({ TopBar: () => null }));
vi.mock("../app/globals.css", () => ({}));

it("generates shared design-system layout utilities from the installed package", async () => {
  const require = createRequire(import.meta.url);
  const postcss = createRequire(require.resolve("@tailwindcss/postcss"))("postcss");
  const from = resolve("app/globals.css");
  const result = await postcss([tailwind({ base: resolve("app") })]).process(
    await readFile(from, "utf8"),
    { from },
  );
  expect(result.css).toContain(".grid-rows-\\[auto_auto\\]");
  expect(result.css).toContain(".backdrop-blur-sm");
  expect(result.css).toContain(".bg-success-muted");
});

it("activates the installed theme on the document root", async () => {
  const { default: Layout } = await import("../app/layout");
  const html = renderToStaticMarkup(
    <Layout>
      <p>Content</p>
    </Layout>,
  );
  expect(html).toContain('data-theme="default"');
});
