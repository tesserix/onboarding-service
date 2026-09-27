import { defineConfig } from "vitest/config";

export default defineConfig({
  resolve: { alias: { "@": new URL(".", import.meta.url).pathname } },
  esbuild: { jsx: "automatic" },
  test: { environment: "jsdom", include: ["**/*.test.ts", "**/*.test.tsx"] },
});
