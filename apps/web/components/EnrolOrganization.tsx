"use client";

import Link from "next/link";
import { useState, type FormEvent } from "react";

import { apiFetch, newIdempotencyKey } from "../lib/api";
import { useApi } from "../lib/useApi";
import type { Organization, Principal, Product } from "../lib/types";
import { Button, Card, ErrorNote, Field, inputClass } from "./ui";

export function EnrolOrganization() {
  const principal = useApi<Principal>("/v1/me");
  const products = useApi<{ products: Product[] }>("/v1/products");
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [enrolled, setEnrolled] = useState<Organization | null>(null);
  const grantable = (products.data?.products ?? []).filter(
    (p) => p.key !== "helivanta" && p.identity === "zitadel" && p.state === "active",
  );

  async function enrol(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (saving) return;
    const body = Object.fromEntries(new FormData(event.currentTarget));
    setSaving(true);
    setError(null);
    setEnrolled(null);
    try {
      const org = await apiFetch<Organization>("/v1/orgs/enrolments", {
        method: "POST",
        idempotencyKey: newIdempotencyKey(),
        body,
      });
      if (
        org.id !== body.tenantId ||
        org.zitadelOrgId !== body.zitadelOrgId ||
        org.state !== "active"
      )
        throw new Error(
          "The returned identity did not match. Review the organisation before retrying.",
        );
      setEnrolled(org);
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setSaving(false);
    }
  }

  if (principal.loading || products.loading) return <p role="status">Loading enrolment options…</p>;
  if (principal.error || products.error)
    return (
      <div className="space-y-4">
        <ErrorNote error={principal.error ?? products.error} />
        <Button
          onClick={() => {
            principal.reload();
            products.reload();
          }}
        >
          Try again
        </Button>
      </div>
    );
  if (principal.data?.kind !== "platform" || !principal.data.roles?.includes("platform-admin"))
    return <ErrorNote error="A platform administrator must enrol existing organisations." />;

  return (
    <div className="mx-auto max-w-3xl space-y-6">
      <p className="text-sm">
        For Helivanta, use{" "}
        <Link href="/orgs/hospital" className="underline underline-offset-4">
          hospital onboarding and adoption
        </Link>{" "}
        to retain the identity configuration and activation receipt.
      </p>
      <Card
        title="Enrol an existing organisation"
        description="Preserve its tenant identity and verify its existing platform entitlement."
      >
        <p className="mb-6 text-sm leading-6 text-muted-foreground">
          Use the existing tenant UUID from the identity provider. This checks the live
          organisation, tenant metadata and exact product grant. It does not create a replacement
          account or reactivate revoked access.
        </p>
        <ErrorNote error={error} />
        {enrolled ? (
          <div className="space-y-4">
            <p role="status">
              Organisation enrolled. Tenant UUID preserved:{" "}
              <span className="break-all font-mono">{enrolled.id}</span>
            </p>
            <Link
              href={`/orgs/${enrolled.id}`}
              className="font-medium underline underline-offset-4"
            >
              Open organisation
            </Link>
          </div>
        ) : grantable.length === 0 ? (
          <p className="text-sm">
            Register an active Zitadel product before enrolling an organisation.{" "}
            <Link href="/products/new" className="underline underline-offset-4">
              Register product
            </Link>
          </p>
        ) : (
          <form onSubmit={enrol} className="mt-4">
            <fieldset disabled={saving} className="grid min-w-0 gap-5 sm:grid-cols-2">
              <Field
                label="Existing tenant UUID"
                hint="The exact tenant_id metadata value; never generate a replacement."
              >
                <input
                  name="tenantId"
                  className={inputClass}
                  required
                  pattern="[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}"
                  maxLength={36}
                  autoComplete="off"
                />
              </Field>
              <Field label="Zitadel organisation ID">
                <input
                  name="zitadelOrgId"
                  className={inputClass}
                  required
                  pattern="[A-Za-z0-9_-]+"
                  maxLength={200}
                  autoComplete="off"
                />
              </Field>
              <Field
                label="Product"
                hint="Must already have an active source project grant for this organisation."
              >
                <select name="productKey" className={inputClass} required defaultValue="">
                  <option value="" disabled>
                    Select a registered product
                  </option>
                  {grantable.map((product) => (
                    <option key={product.key} value={product.key}>
                      {product.displayName}
                    </option>
                  ))}
                </select>
              </Field>
              <Field
                label="Organisation display name"
                hint="An administrative label; not identity or licence evidence."
              >
                <input
                  name="name"
                  className={inputClass}
                  required
                  maxLength={200}
                  autoComplete="off"
                />
              </Field>
              <div className="sm:col-span-2">
                <Field
                  label="Enrolment reason"
                  hint="Recorded in the audit trail. Do not include patient, banking or private contact data."
                >
                  <textarea
                    name="reason"
                    className={`${inputClass} min-h-24 resize-y`}
                    required
                    maxLength={500}
                  />
                </Field>
              </div>
              <div className="sm:col-span-2">
                <Button type="submit" disabled={saving}>
                  {saving ? "Checking and enrolling…" : "Verify and enrol existing organisation"}
                </Button>
              </div>
            </fieldset>
          </form>
        )}
      </Card>
      <Card title="Trading approval is separate">
        <p className="text-sm leading-6 text-muted-foreground">
          Enrolment establishes the platform binding only. Licences, country policy, bilateral
          consent and access to private records remain separate requirements.
        </p>
      </Card>
    </div>
  );
}
