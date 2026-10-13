import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { useApi } from "./useApi";

let container: HTMLDivElement;
let root: Root;
const pending: Array<(response: Response) => void> = [];
function Fixture({ path }: { path: string | null }) {
  const state = useApi<{ name: string }>(path);
  return (
    <>
      <output>
        {JSON.stringify({ data: state.data, error: state.error, loading: state.loading })}
      </output>
      <button onClick={state.reload}>Reload</button>
    </>
  );
}
function state() {
  return JSON.parse(container.querySelector("output")!.textContent!);
}
async function resolveRequest(index: number, name: string) {
  await act(async () => {
    pending[index](
      new Response(JSON.stringify({ name }), { headers: { "Content-Type": "application/json" } }),
    );
  });
}
beforeEach(() => {
  pending.length = 0;
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  vi.stubGlobal(
    "fetch",
    vi.fn(() => new Promise<Response>((resolve) => pending.push(resolve))),
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

it("does not display one organization's data while another organization loads", async () => {
  await act(async () => root.render(<Fixture path="/v1/orgs/a" />));
  await resolveRequest(0, "Organization A");
  expect(state().data.name).toBe("Organization A");
  await act(async () => root.render(<Fixture path="/v1/orgs/b" />));
  expect(state()).toEqual({ data: null, error: null, loading: true });
  await resolveRequest(1, "Organization B");
  expect(state()).toEqual({ data: { name: "Organization B" }, error: null, loading: false });
});

it("ignores a previous route's request if it finishes after the current route", async () => {
  await act(async () => root.render(<Fixture path="/v1/orgs/a" />));
  await act(async () => root.render(<Fixture path="/v1/orgs/b" />));
  await resolveRequest(1, "Organization B");
  await resolveRequest(0, "Organization A");
  expect(state().data.name).toBe("Organization B");
});

it("keeps current data visible during a manual refresh", async () => {
  await act(async () => root.render(<Fixture path="/v1/orgs/a" />));
  await resolveRequest(0, "Organization A");
  await act(async () => container.querySelector("button")!.click());
  expect(state()).toEqual({ data: { name: "Organization A" }, error: null, loading: true });
  await resolveRequest(1, "Updated organization A");
  expect(state().loading).toBe(false);
  expect(state().data.name).toBe("Updated organization A");
});
