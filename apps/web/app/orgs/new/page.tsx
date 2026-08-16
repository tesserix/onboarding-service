"use client";

import { useRouter } from "next/navigation";
import { useState } from "react";

import { Button, Card, ErrorNote, Field, inputClass } from "@/components/ui";
import { apiFetch, newIdempotencyKey } from "@/lib/api";
import { useApi } from "@/lib/useApi";
import type { Organization, Product } from "@/lib/types";

export default function NewOrgPage() {
  const router = useRouter();
  const products = useApi<{ products: Product[] }>("/v1/products");
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [form, setForm] = useState({
    name: "",
    ownerEmail: "",
    ownerName: "",
    billingEmail: "",
    supportEmail: "",
    plan: "",
    seats: "",
    region: "",
    externalRef: "",
  });
  const [granted, setGranted] = useState<string[]>([]);

  const set = (field: keyof typeof form) => (value: string) => setForm({ ...form, [field]: value });

  // Only Zitadel products can hold an org, so a GIP product is not offered here
  // rather than being offered and then refused by the API.
  const grantable = (products.data?.products ?? []).filter((p) => p.identity === "zitadel");

  async function create() {
    setSaving(true);
    setError(null);
    try {
      const org = await apiFetch<Organization>("/v1/orgs", {
        method: "POST",
        idempotencyKey: newIdempotencyKey(),
        body: {
          name: form.name,
          ownerEmail: form.ownerEmail,
          ownerName: form.ownerName || undefined,
          products: granted,
          billingEmail: form.billingEmail || undefined,
          supportEmail: form.supportEmail || undefined,
          plan: form.plan || undefined,
          seats: form.seats ? Number(form.seats) : undefined,
          region: form.region || undefined,
          externalRef: form.externalRef || undefined,
        },
      });
      router.push(`/orgs/${org.id}`);
    } catch (err) {
      setError((err as Error).message);
      setSaving(false);
    }
  }

  return (
    <div className="space-y-6">
      <ErrorNote error={error ?? products.error} />
      <Card title="New organization">
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <Field label="Name">
            <input className={inputClass} value={form.name} onChange={(e) => set("name")(e.target.value)} />
          </Field>
          <Field label="Owner email" hint="Becomes ORG_OWNER and the only person who can invite others.">
            <input
              className={inputClass}
              type="email"
              value={form.ownerEmail}
              onChange={(e) => set("ownerEmail")(e.target.value)}
            />
          </Field>
          <Field label="Owner name">
            <input className={inputClass} value={form.ownerName} onChange={(e) => set("ownerName")(e.target.value)} />
          </Field>
          <Field label="Billing email" hint="So an incident does not start with &quot;who do we tell&quot;.">
            <input
              className={inputClass}
              type="email"
              value={form.billingEmail}
              onChange={(e) => set("billingEmail")(e.target.value)}
            />
          </Field>
          <Field label="Support email">
            <input
              className={inputClass}
              type="email"
              value={form.supportEmail}
              onChange={(e) => set("supportEmail")(e.target.value)}
            />
          </Field>
          <Field label="Plan" hint="A label, not an entitlement — billing lives elsewhere.">
            <input className={inputClass} value={form.plan} onChange={(e) => set("plan")(e.target.value)} />
          </Field>
          <Field label="Seats">
            <input
              className={inputClass}
              type="number"
              min={0}
              value={form.seats}
              onChange={(e) => set("seats")(e.target.value)}
            />
          </Field>
          <Field label="Region">
            <input className={inputClass} value={form.region} onChange={(e) => set("region")(e.target.value)} />
          </Field>
          <Field label="External reference" hint="Your CRM or signup id, so reconciliation never needs a name match.">
            <input
              className={inputClass}
              value={form.externalRef}
              onChange={(e) => set("externalRef")(e.target.value)}
            />
          </Field>
        </div>
      </Card>

      <Card title="Products">
        <div className="space-y-2">
          {grantable.map((product) => (
            <label key={product.key} className="flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                checked={granted.includes(product.key)}
                onChange={(e) =>
                  setGranted(
                    e.target.checked
                      ? [...granted, product.key]
                      : granted.filter((key) => key !== product.key),
                  )
                }
              />
              <span>{product.displayName}</span>
              <span className="font-mono text-xs text-muted-foreground">{product.key}</span>
            </label>
          ))}
        </div>
        <div className="mt-4">
          <Button onClick={create} disabled={saving || !form.name || !form.ownerEmail}>
            {saving ? "Creating…" : "Create organization"}
          </Button>
        </div>
      </Card>
    </div>
  );
}
