import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    server: { deps: { inline: [/@tesserix\//] } },
    environment: "jsdom",
    include: ["**/*.test.ts", "**/*.test.tsx"],
  },
});
