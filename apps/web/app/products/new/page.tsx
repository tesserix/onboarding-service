"use client";

import Link from "next/link";
import { useState } from "react";

import { Button, Card, CopyButton, ErrorNote, Field, inputClass } from "@/components/ui";
import { apiFetch, newIdempotencyKey } from "@/lib/api";
import { productManifests } from "@/lib/manifest";
import type { Product } from "@/lib/types";

type Form = {
  key: string;
  displayName: string;
  zitadelProjectId: string;
  machineUserId: string;
  oidcClientId: string;
  hosts: string;
  identity: "zitadel" | "gip";
  tenantSecrets: "openbao" | "none";
  tenancy: "org" | "ephemeral+org";
  roles: string;
  dataResidency: string;
};

const empty: Form = {
  key: "",
  displayName: "",
  zitadelProjectId: "",
  machineUserId: "",
  oidcClientId: "",
  hosts: "",
  identity: "zitadel",
  tenantSecrets: "openbao",
  tenancy: "org",
  roles: "",
  dataResidency: "in",
};

const split = (value: string) =>
  value
    .split(/[\s,]+/)
    .map((item) => item.trim())
    .filter(Boolean);

const steps = ["Identity", "Capabilities", "Review", "Integration review"];

export default function NewProductPage() {
  const [step, setStep] = useState(0);
  const [form, setForm] = useState(empty);
  const [created, setCreated] = useState<Product | null>(null);
  const [acked, setAcked] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const set = (field: keyof Form) => (value: string) => setForm({ ...form, [field]: value });
  const hosts = split(form.hosts);
  const roles = split(form.roles);
  const snippets = created
    ? productManifests({ ...created, machineUserId: form.machineUserId })
    : [];

  async function create() {
    setError(null);
    try {
      const product = await apiFetch<Product>("/v1/products", {
        method: "POST",
        idempotencyKey: newIdempotencyKey(),
        body: {
          key: form.key,
          displayName: form.displayName,
          zitadelProjectId: form.zitadelProjectId,
          machineUserId: form.machineUserId,
          oidcClientId: form.oidcClientId || undefined,
          hosts,
          identity: form.identity,
          tenantSecrets: form.tenantSecrets,
          tenancy: form.tenancy,
          roles,
          dataResidency: form.dataResidency,
        },
      });
      setCreated(product);
      setStep(3);
    } catch (err) {
      setError((err as Error).message);
    }
  }

  async function acknowledge() {
    setError(null);
    try {
      await apiFetch(`/v1/products/${encodeURIComponent(form.key)}/manifests-acknowledged`, {
        method: "POST",
      });
      setAcked(true);
    } catch (err) {
      setError((err as Error).message);
    }
  }

  return (
    <div className="space-y-6">
      <ol className="flex gap-2 text-sm">
        {steps.map((label, index) => (
          <li
            key={label}
            className={`rounded-full px-3 py-1 ${
              index === step
                ? "bg-primary text-primary-foreground"
                : "bg-muted text-muted-foreground"
            }`}
          >
            {index + 1}. {label}
          </li>
        ))}
      </ol>

      <ErrorNote error={error} />

      {step === 0 && (
        <Card title="Identity">
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <Field
              label="Key"
              hint="Lowercase, stable — it names the namespace, the roles and every secret."
            >
              <input
                className={inputClass}
                value={form.key}
                onChange={(e) => set("key")(e.target.value)}
              />
            </Field>
            <Field label="Display name">
              <input
                className={inputClass}
                value={form.displayName}
                onChange={(e) => set("displayName")(e.target.value)}
              />
            </Field>
            <Field label="Zitadel project id">
              <input
                className={inputClass}
                value={form.zitadelProjectId}
                onChange={(e) => set("zitadelProjectId")(e.target.value)}
              />
            </Field>
            <Field
              label="Machine user id"
              hint="The svc- user whose JWT the product calls this API with."
            >
              <input
                className={inputClass}
                value={form.machineUserId}
                onChange={(e) => set("machineUserId")(e.target.value)}
              />
            </Field>
            <Field label="OIDC client id" hint="Optional until the web app exists.">
              <input
                className={inputClass}
                value={form.oidcClientId}
                onChange={(e) => set("oidcClientId")(e.target.value)}
              />
            </Field>
            <Field
              label="Hosts"
              hint="Comma separated. Drives the redirect URIs in the git snippet."
            >
              <input
                className={inputClass}
                value={form.hosts}
                onChange={(e) => set("hosts")(e.target.value)}
              />
            </Field>
          </div>
          <div className="mt-4">
            <Button onClick={() => setStep(1)} disabled={!form.key || !form.displayName}>
              Next
            </Button>
          </div>
        </Card>
      )}

      {step === 1 && (
        <Card title="Capabilities">
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <Field
              label="Where do its users authenticate?"
              hint="A GIP product gets a products row and the ledger only — the org endpoints refuse it."
            >
              <select
                className={inputClass}
                value={form.identity}
                onChange={(e) => set("identity")(e.target.value)}
              >
                <option value="zitadel">Zitadel</option>
                <option value="gip">Google Identity Platform</option>
              </select>
            </Field>
            <Field
              label="Where do its customers' secrets live?"
              hint="Application and operational secrets belong in environment-scoped OpenBao paths. The product registry stores configuration, never secret values."
            >
              <select
                className={inputClass}
                value={form.tenantSecrets}
                onChange={(e) => set("tenantSecrets")(e.target.value)}
              >
                <option value="openbao">OpenBao (per tenant)</option>
                <option value="none">None — it holds nothing on a customer&apos;s behalf</option>
              </select>
            </Field>
            <Field
              label="Tenancy"
              hint="ephemeral+org means anonymous usage can later be claimed into an org."
            >
              <select
                className={inputClass}
                value={form.tenancy}
                onChange={(e) => set("tenancy")(e.target.value)}
              >
                <option value="org">org</option>
                <option value="ephemeral+org">ephemeral+org</option>
              </select>
            </Field>
            <Field label="Data residency">
              <select
                className={inputClass}
                value={form.dataResidency}
                onChange={(e) => set("dataResidency")(e.target.value)}
              >
                <option value="in">in</option>
                <option value="other">other</option>
              </select>
            </Field>
            <Field
              label="Project roles"
              hint="Comma separated, deliberately short. Row-level rules belong to OpenFGA."
            >
              <input
                className={inputClass}
                value={form.roles}
                onChange={(e) => set("roles")(e.target.value)}
              />
            </Field>
          </div>
          <div className="mt-4 flex gap-2">
            <Button variant="secondary" onClick={() => setStep(0)}>
              Back
            </Button>
            <Button onClick={() => setStep(2)}>Next</Button>
          </div>
        </Card>
      )}

      {step === 2 && (
        <Card title="Review">
          <dl className="grid grid-cols-1 gap-2 text-sm sm:grid-cols-2">
            {Object.entries({ ...form, hosts: hosts.join(", "), roles: roles.join(", ") }).map(
              ([key, value]) => (
                <div key={key} className="flex justify-between border-b border-border py-1">
                  <dt className="text-muted-foreground">{key}</dt>
                  <dd className="font-mono text-xs">{value || "—"}</dd>
                </div>
              ),
            )}
          </dl>
          <div className="mt-4 flex gap-2">
            <Button variant="secondary" onClick={() => setStep(1)}>
              Back
            </Button>
            <Button onClick={create}>Create product</Button>
          </div>
        </Card>
      )}

      {step === 3 && created && (
        <div className="space-y-4">
          <Card title={`${created.displayName} is registered`}>
            <p className="text-sm text-muted-foreground">
              The registry entry is saved. The record below preserves its reviewed identifiers; it
              does not deploy infrastructure. Verify the existing identity and authorization
              configuration in GitOps before onboarding customers.
            </p>
          </Card>

          {created.key === "helivanta" && (
            <Link
              href="/orgs/hospital"
              className="block text-sm font-medium underline underline-offset-4"
            >
              Onboard or adopt a hospital identity
            </Link>
          )}
          {snippets.map((snippet) => (
            <Card
              key={snippet.path}
              title={snippet.title}
              action={<CopyButton value={snippet.body} label="Copy" />}
            >
              <p className="mb-2 font-mono text-xs text-muted-foreground">{snippet.path}</p>
              <pre className="overflow-x-auto rounded-md bg-foreground p-3 text-xs text-background">
                {snippet.body}
              </pre>
            </Card>
          ))}

          <Card title="Integration review">
            {acked ? (
              <p className="text-sm text-emerald-600">
                Review acknowledgement recorded in the audit ledger.{" "}
                <Link href="/products">Back to products</Link>
              </p>
            ) : (
              <Button onClick={acknowledge}>Record review acknowledgement</Button>
            )}
          </Card>
        </div>
      )}
    </div>
  );
}
