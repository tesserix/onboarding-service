"use client";

import Link from "next/link";
import { useRef, useState, type FormEvent } from "react";
import { apiFetch, newIdempotencyKey } from "../lib/api";
import type { Principal } from "../lib/types";
import { useApi } from "../lib/useApi";
import { Button, Card, ErrorNote, Field, inputClass } from "./ui";

type HospitalClaim = {
  spec: {
    product: string;
    tenantId: string;
    displayName: string;
    initialAdminUserId: string;
    organizationId?: string;
    preserveLoginPolicy?: boolean;
  };
  status: {
    archiveObject?: string;
    archiveGeneration?: string;
    conditions?: Array<{ type: string; reason: string }>;
  };
};

export function HospitalOnboarding({ initialTenantId = "" }: { initialTenantId?: string }) {
  const principal = useApi<Principal>("/v1/me");
  const [mode, setMode] = useState("new");
  const [tenantId, setTenantId] = useState(initialTenantId);
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const request = useRef<{ body: string; key: string } | null>(null);
  const isAdmin =
    principal.data?.kind === "platform" && principal.data.roles?.includes("platform-admin");
  const status = useApi<{ state: string; onboarding: HospitalClaim }>(
    isAdmin && tenantId
      ? `/v1/hospital-onboardings/${encodeURIComponent(tenantId)}?product=helivanta`
      : null,
  );
  const matched =
    status.data?.onboarding.spec.product === "helivanta" &&
    status.data.onboarding.spec.tenantId === tenantId;
  const receipt = matched ? status.data?.onboarding.status : null;
  const complete =
    matched &&
    !status.error &&
    !status.loading &&
    status.data?.state === "complete" &&
    !!receipt?.archiveObject &&
    !!receipt.archiveGeneration;

  async function start(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (saving) return;
    const fields = new FormData(event.currentTarget);
    const body = {
      product: "helivanta",
      displayName: String(fields.get("displayName")).trim(),
      initialAdminUserId: String(fields.get("initialAdminUserId")).trim(),
      ...(mode === "adopt"
        ? {
            tenantId: String(fields.get("tenantId")).trim(),
            organizationId: String(fields.get("organizationId")).trim(),
            preserveLoginPolicy: fields.get("preserveLoginPolicy") === "on",
          }
        : {}),
    };
    const serialized = JSON.stringify(body);
    if (request.current?.body !== serialized)
      request.current = { body: serialized, key: newIdempotencyKey() };
    setSaving(true);
    setError(null);
    try {
      const result = await apiFetch<HospitalClaim>("/v1/hospital-onboardings", {
        method: "POST",
        body,
        idempotencyKey: request.current!.key,
      });
      if (
        result.spec.product !== body.product ||
        result.spec.displayName !== body.displayName ||
        result.spec.initialAdminUserId !== body.initialAdminUserId ||
        !result.spec.tenantId ||
        (mode === "adopt" &&
          (result.spec.tenantId !== body.tenantId ||
            result.spec.organizationId !== body.organizationId))
      )
        throw new Error(
          "The returned identity did not match this request. Review before retrying.",
        );
      setTenantId(result.spec.tenantId);
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setSaving(false);
    }
  }

  if (principal.loading) return <p role="status">Loading hospital onboarding…</p>;
  if (principal.error)
    return (
      <>
        <ErrorNote error={principal.error} />
        <Button onClick={principal.reload}>Try again</Button>
      </>
    );
  if (!isAdmin)
    return (
      <ErrorNote error="A platform administrator must onboard or adopt hospital identities here." />
    );

  return (
    <div className="mx-auto max-w-3xl space-y-6">
      <Card
        title="Helivanta hospital onboarding"
        description="Create an identity boundary or adopt an existing hospital without replacing its accounts."
      >
        <p className="mb-6 text-sm leading-6 text-muted-foreground">
          A hospital group can share one identity organization across its branches, pharmacies and
          labs. Independently administered entities use separate organizations. Shared staff keep
          one login with explicit membership and resource permissions in each tenant.
        </p>
        <ErrorNote
          error={
            error ??
            status.error ??
            (status.data && !matched ? "The returned identity did not match this tenant." : null)
          }
        />
        {tenantId ? (
          <div className="space-y-4">
            <p role="status">{complete ? "Onboarding complete" : "Onboarding pending"}</p>
            <p className="break-all text-sm">
              Tenant UUID: <span className="font-mono">{tenantId}</span>
            </p>
            {matched && <p className="text-sm">{status.data?.onboarding.spec.displayName}</p>}
            {receipt?.conditions
              ?.filter((c) => c.type === "Ready")
              .map((c) => (
                <p key={c.type} className="text-sm">
                  Controller: {c.reason}
                </p>
              ))}
            {receipt?.archiveObject && (
              <p className="break-all text-xs">
                Archive: {receipt.archiveObject}
                <br />
                Generation: {receipt.archiveGeneration ?? "pending"}
              </p>
            )}
            <button
              type="button"
              data-action="check-status"
              disabled={status.loading}
              onClick={status.reload}
              className="font-medium underline underline-offset-4 disabled:opacity-50"
            >
              {status.loading ? "Checking…" : "Check status"}
            </button>
            <div className="flex flex-wrap gap-4 text-sm">
              <Link
                href={`/orgs/hospital?tenantId=${encodeURIComponent(tenantId)}`}
                className="underline underline-offset-4"
              >
                Resume this onboarding
              </Link>
              {complete && (
                <Link href={`/orgs/${tenantId}`} className="underline underline-offset-4">
                  Open organization
                </Link>
              )}
            </div>
            <p className="text-sm text-muted-foreground">
              Completion requires the identity, product membership, permission projection and
              retained archive receipt. A pending request is safe to check again.
            </p>
          </div>
        ) : (
          <form onSubmit={start}>
            <fieldset disabled={saving} className="grid min-w-0 gap-5 sm:grid-cols-2">
              <Field label="Workflow">
                <select
                  name="mode"
                  className={inputClass}
                  value={mode}
                  onChange={(e) => setMode(e.target.value)}
                >
                  <option value="new">New hospital or group</option>
                  <option value="adopt">Adopt existing hospital</option>
                </select>
              </Field>
              <Field label="Organization display name">
                <input
                  name="displayName"
                  required
                  maxLength={200}
                  className={inputClass}
                  autoComplete="off"
                />
              </Field>
              <div className="sm:col-span-2">
                <Field
                  label="Existing verified administrator user ID"
                  hint="An active human Zitadel user with a verified email. Adoption requires their existing Helivanta tenant administrator membership."
                >
                  <input
                    name="initialAdminUserId"
                    required
                    pattern="[A-Za-z0-9_-]+"
                    maxLength={200}
                    className={inputClass}
                    autoComplete="off"
                  />
                </Field>
              </div>
              {mode === "adopt" && (
                <>
                  <Field
                    label="Existing tenant UUID"
                    hint="The exact tenant_id metadata; retain the current value."
                  >
                    <input
                      name="tenantId"
                      required
                      pattern="[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}"
                      maxLength={36}
                      className={inputClass}
                      autoComplete="off"
                    />
                  </Field>
                  <Field label="Existing Zitadel organization ID">
                    <input
                      name="organizationId"
                      required
                      pattern="[A-Za-z0-9_-]+"
                      maxLength={200}
                      className={inputClass}
                      autoComplete="off"
                    />
                  </Field>
                  <label className="flex items-center gap-3 text-sm sm:col-span-2">
                    <input type="checkbox" name="preserveLoginPolicy" defaultChecked />
                    Preserve the existing login and domain policies for a staged MFA cutover
                  </label>
                </>
              )}
              <div className="sm:col-span-2">
                <Button type="submit" disabled={saving}>
                  {saving
                    ? "Requesting onboarding…"
                    : mode === "adopt"
                      ? "Adopt hospital identity"
                      : "Onboard new hospital"}
                </Button>
              </div>
            </fieldset>
          </form>
        )}
      </Card>
      <Card title="Access stays explicit">
        <p className="text-sm leading-6 text-muted-foreground">
          Identity ownership does not grant access to clinical records. Helivanta membership and
          permissions control each hospital, branch, pharmacy and lab. New identities enforce MFA
          and verified domains; adopting existing policies requires a separately reviewed security
          cutover.
        </p>
      </Card>
    </div>
  );
}
