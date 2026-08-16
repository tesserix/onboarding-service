"use client";

import { useState } from "react";

import { AuditTable } from "@/components/AuditTable";
import { Card, ErrorNote, inputClass, PageTitle } from "@/components/ui";
import { useApi } from "@/lib/useApi";
import type { AuditEvent } from "@/lib/types";

export default function AuditPage() {
  const [filters, setFilters] = useState({ orgId: "", productKey: "", actor: "", action: "" });

  const query = new URLSearchParams(
    Object.entries(filters).filter(([, v]) => v !== ""),
  ).toString();
  const { data, error } = useApi<{ events: AuditEvent[] }>(`/v1/audit${query ? `?${query}` : ""}`);

  return (
    <div className="space-y-8">
      <PageTitle title="Audit" description="The append-only ledger: who changed what, when, and from which request." />
      <ErrorNote error={error} />
      <Card title="Filter">
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-4">
          {(["orgId", "productKey", "actor", "action"] as const).map((field) => (
            <input
              key={field}
              className={inputClass}
              placeholder={field}
              value={filters[field]}
              onChange={(e) => setFilters({ ...filters, [field]: e.target.value })}
            />
          ))}
        </div>
      </Card>
      <Card title="Ledger">
        <AuditTable events={data?.events ?? []} />
      </Card>
    </div>
  );
}
