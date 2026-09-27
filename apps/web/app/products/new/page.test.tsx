import { renderToString } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";

import NewProductPage from "./page";

vi.mock("@/components/ui", () => ({ Button: () => null, Card: () => null, CopyButton: () => null, ErrorNote: () => null, Field: () => null, inputClass: "" }));

describe("new product form", () => {
  it("renders the empty form without generating invalid manifests", () => {
    expect(() => renderToString(<NewProductPage />)).not.toThrow();
  });
});
