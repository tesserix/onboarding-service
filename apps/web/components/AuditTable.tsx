"use client";

import { Badge, Empty } from "./ui";
import type { AuditEvent } from "@/lib/types";

const tone = { success: "ok", denied: "warn", error: "bad" } as const;

export function AuditTable({ events }: { events: AuditEvent[] }) {
  if (events.length === 0) return <Empty>No events.</Empty>;

  return (
    <div className="overflow-x-auto">
      <table className="w-full text-left text-sm">
        <thead className="text-xs uppercase tracking-wide text-muted-foreground">
          <tr>
            <th className="py-2 pr-4">Seq</th>
            <th className="py-2 pr-4">When</th>
            <th className="py-2 pr-4">Actor</th>
            <th className="py-2 pr-4">Action</th>
            <th className="py-2 pr-4">Target</th>
            <th className="py-2 pr-4">Outcome</th>
            <th className="py-2">Request</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-border">
          {events.map((e) => (
            <tr key={e.seq}>
              <td className="py-2 pr-4 font-mono text-xs text-muted-foreground">{e.seq}</td>
              <td className="py-2 pr-4 whitespace-nowrap">
                {new Date(e.occurredAt).toLocaleString()}
              </td>
              <td className="py-2 pr-4">
                <span className="font-mono text-xs">{e.actorId}</span>
                <span className="ml-1 text-xs text-muted-foreground">({e.actorType})</span>
              </td>
              <td className="py-2 pr-4 font-medium">{e.action}</td>
              <td className="py-2 pr-4 font-mono text-xs text-muted-foreground">
                {e.target ?? "—"}
              </td>
              <td className="py-2 pr-4">
                <Badge tone={tone[e.outcome] ?? "muted"}>{e.outcome}</Badge>
              </td>
              <td className="py-2 font-mono text-xs text-muted-foreground/70">{e.requestId}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
