"use client";

import Link from "next/link";

import { AuditTable } from "@/components/AuditTable";
import { Badge, Card, Empty, ErrorNote } from "@/components/ui";
import { useApi } from "@/lib/useApi";
import type { AuditEvent, Organization, Product } from "@/lib/types";

export default function Dashboard() {
  const orgs = useApi<{ organizations: Organization[] }>("/v1/orgs");
  const products = useApi<{ products: Product[] }>("/v1/products");
  const audit = useApi<{ events: AuditEvent[] }>("/v1/audit?limit=20");

  const all = orgs.data?.organizations ?? [];
  const active = all.filter((o) => o.state === "active").length;

  return (
    <div className="space-y-6">
      <ErrorNote error={orgs.error ?? products.error ?? audit.error} />

      <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
        <Card title="Organizations">
          <p className="text-3xl font-semibold">{all.length}</p>
          <p className="text-sm text-slate-500">
            {active} active · {all.length - active} inactive
          </p>
        </Card>
        <Card title="Products">
          <p className="text-3xl font-semibold">{products.data?.products.length ?? 0}</p>
          <p className="text-sm text-slate-500">onboarded projects</p>
        </Card>
        <Card title="Recent activity">
          <p className="text-3xl font-semibold">{audit.data?.events.length ?? 0}</p>
          <p className="text-sm text-slate-500">events in the last page of the ledger</p>
        </Card>
      </div>

      <Card
        title="Products"
        action={
          <Link href="/products/new" className="text-sm text-slate-600 hover:text-slate-900">
            New product
          </Link>
        }
      >
        {products.data?.products.length ? (
          <ul className="divide-y divide-slate-100">
            {products.data.products.map((p) => (
              <li key={p.key} className="flex items-center justify-between py-2">
                <div>
                  <Link href={`/products`} className="text-sm font-medium">
                    {p.displayName}
                  </Link>
                  <p className="text-xs text-slate-500">{p.key}</p>
                </div>
                <div className="flex gap-2">
                  <Badge tone="muted">{p.identity}</Badge>
                  <Badge tone={p.tenantSecrets === "openbao" ? "ok" : "muted"}>
                    secrets: {p.tenantSecrets}
                  </Badge>
                </div>
              </li>
            ))}
          </ul>
        ) : (
          <Empty>No products yet.</Empty>
        )}
      </Card>

      {/* The audit strip is on the landing page deliberately: a ledger nobody
          sees is decoration. */}
      <Card
        title="Last 20 audit events"
        action={
          <Link href="/audit" className="text-sm text-slate-600 hover:text-slate-900">
            All events
          </Link>
        }
      >
        <AuditTable events={audit.data?.events ?? []} />
      </Card>
    </div>
  );
}
