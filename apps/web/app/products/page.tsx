"use client";

import Link from "next/link";

import { Badge, Card, Empty, ErrorNote, PageTitle } from "@/components/ui";
import { useApi } from "@/lib/useApi";
import type { Product } from "@/lib/types";

export default function ProductsPage() {
  const { data, error } = useApi<{ products: Product[] }>("/v1/products");
  const products = data?.products ?? [];

  return (
    <div className="space-y-8">
      <PageTitle title="Products" description="Projects onboarded to the platform and how each one carries identity and tenant secrets." />
      <ErrorNote error={error} />
      <Card
        title="Products"
        action={
          <Link href="/products/new" className="text-sm text-muted-foreground transition-colors hover:text-foreground">
            New product
          </Link>
        }
      >
        {products.length === 0 ? (
          <Empty>No products yet.</Empty>
        ) : (
          <table className="w-full text-left text-sm">
            <thead className="text-xs uppercase tracking-wide text-muted-foreground">
              <tr>
                <th className="py-2 pr-4">Product</th>
                <th className="py-2 pr-4">Identity</th>
                <th className="py-2 pr-4">Customer secrets</th>
                <th className="py-2 pr-4">Tenancy</th>
                <th className="py-2 pr-4">Roles</th>
                <th className="py-2">Hosts</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border">
              {products.map((p) => (
                <tr key={p.key}>
                  <td className="py-2 pr-4">
                    <span className="font-medium">{p.displayName}</span>
                    <span className="block font-mono text-xs text-muted-foreground">{p.key}</span>
                  </td>
                  <td className="py-2 pr-4">
                    <Badge tone={p.identity === "zitadel" ? "ok" : "muted"}>{p.identity}</Badge>
                  </td>
                  <td className="py-2 pr-4">
                    <Badge tone={p.tenantSecrets === "openbao" ? "ok" : "muted"}>{p.tenantSecrets}</Badge>
                  </td>
                  <td className="py-2 pr-4 text-muted-foreground">{p.tenancy}</td>
                  <td className="py-2 pr-4 font-mono text-xs text-muted-foreground">{p.roles.join(", ") || "—"}</td>
                  <td className="py-2 font-mono text-xs text-muted-foreground">{p.hosts?.join(", ") || "—"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Card>
    </div>
  );
}
