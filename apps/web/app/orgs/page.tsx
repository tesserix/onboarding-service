"use client";

import Link from "next/link";
import { useState } from "react";

import { Badge, Card, Empty, ErrorNote, inputClass, PageTitle } from "@/components/ui";
import { useApi } from "@/lib/useApi";
import type { Organization } from "@/lib/types";

export default function OrgsPage() {
  const [search, setSearch] = useState("");
  const { data, error } = useApi<{ organizations: Organization[] }>("/v1/orgs");

  const needle = search.trim().toLowerCase();
  const orgs = (data?.organizations ?? []).filter(
    (o) =>
      needle === "" ||
      o.name.toLowerCase().includes(needle) ||
      (o.primaryDomain ?? "").toLowerCase().includes(needle) ||
      (o.externalRef ?? "").toLowerCase().includes(needle),
  );

  return (
    <div className="space-y-8">
      <PageTitle title="Organizations" description="Every customer organization, and which product created it." />
      <ErrorNote error={error} />
      <Card
        title="Organizations"
        action={
          <Link href="/orgs/new" className="text-sm text-muted-foreground transition-colors hover:text-foreground">
            New organization
          </Link>
        }
      >
        <input
          className={`${inputClass} mb-4`}
          placeholder="Search by name, domain or external reference"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
        />
        {orgs.length === 0 ? (
          <Empty>No organizations.</Empty>
        ) : (
          <table className="w-full text-left text-sm">
            <thead className="text-xs uppercase tracking-wide text-muted-foreground">
              <tr>
                <th className="py-2 pr-4">Name</th>
                <th className="py-2 pr-4">Domain</th>
                <th className="py-2 pr-4">Onboarded by</th>
                <th className="py-2 pr-4">Plan</th>
                <th className="py-2">State</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border">
              {orgs.map((org) => (
                <tr key={org.id}>
                  <td className="py-2 pr-4">
                    <Link href={`/orgs/${org.id}`} className="font-medium hover:underline">
                      {org.name}
                    </Link>
                    <span className="block font-mono text-xs text-muted-foreground">{org.id}</span>
                  </td>
                  <td className="py-2 pr-4 text-muted-foreground">{org.primaryDomain ?? "—"}</td>
                  <td className="py-2 pr-4 text-muted-foreground">{org.createdByProduct || "platform"}</td>
                  <td className="py-2 pr-4 text-muted-foreground">{org.plan ?? "—"}</td>
                  <td className="py-2">
                    <Badge tone={org.state === "active" ? "ok" : "muted"}>{org.state}</Badge>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Card>
    </div>
  );
}
