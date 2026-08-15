"use client";

import { useState, type ReactNode } from "react";

export function Card({ title, action, children }: { title?: string; action?: ReactNode; children: ReactNode }) {
  return (
    <section className="rounded-lg border border-slate-200 bg-white shadow-sm">
      {(title || action) && (
        <header className="flex items-center justify-between border-b border-slate-200 px-4 py-3">
          <h2 className="text-sm font-semibold text-slate-700">{title}</h2>
          {action}
        </header>
      )}
      <div className="p-4">{children}</div>
    </section>
  );
}

export function Button({
  children,
  onClick,
  type = "button",
  variant = "primary",
  disabled,
}: {
  children: ReactNode;
  onClick?: () => void;
  type?: "button" | "submit";
  variant?: "primary" | "secondary" | "danger";
  disabled?: boolean;
}) {
  const styles = {
    primary: "bg-slate-900 text-white hover:bg-slate-700",
    secondary: "border border-slate-300 bg-white text-slate-700 hover:bg-slate-100",
    danger: "bg-red-600 text-white hover:bg-red-700",
  }[variant];
  return (
    <button
      type={type}
      onClick={onClick}
      disabled={disabled}
      className={`rounded-md px-3 py-1.5 text-sm font-medium disabled:opacity-40 ${styles}`}
    >
      {children}
    </button>
  );
}

export function Field({
  label,
  hint,
  children,
}: {
  label: string;
  hint?: string;
  children: ReactNode;
}) {
  return (
    <label className="block space-y-1">
      <span className="text-sm font-medium text-slate-700">{label}</span>
      {children}
      {hint && <span className="block text-xs text-slate-500">{hint}</span>}
    </label>
  );
}

export const inputClass =
  "w-full rounded-md border border-slate-300 px-3 py-1.5 text-sm focus:border-slate-500 focus:outline-none";

export function Badge({ tone, children }: { tone: "ok" | "warn" | "bad" | "muted"; children: ReactNode }) {
  const styles = {
    ok: "bg-emerald-100 text-emerald-800",
    warn: "bg-amber-100 text-amber-800",
    bad: "bg-red-100 text-red-800",
    muted: "bg-slate-100 text-slate-600",
  }[tone];
  return <span className={`rounded-full px-2 py-0.5 text-xs font-medium ${styles}`}>{children}</span>;
}

export function ErrorNote({ error }: { error: string | null }) {
  if (!error) return null;
  return (
    <p role="alert" className="rounded-md bg-red-50 px-3 py-2 text-sm text-red-800">
      {error}
    </p>
  );
}

export function Empty({ children }: { children: ReactNode }) {
  return <p className="py-6 text-center text-sm text-slate-500">{children}</p>;
}

// Every destructive action names what it destroys and makes the operator type
// it: revoking a grant signs out a customer's entire staff.
export function ConfirmDestructive({
  what,
  confirmWord,
  onConfirm,
  children,
}: {
  what: string;
  confirmWord: string;
  onConfirm: () => void;
  children: ReactNode;
}) {
  const [open, setOpen] = useState(false);
  const [typed, setTyped] = useState("");

  if (!open) {
    return (
      <Button variant="danger" onClick={() => setOpen(true)}>
        {children}
      </Button>
    );
  }
  return (
    <div className="space-y-2 rounded-md border border-red-200 bg-red-50 p-3">
      <p className="text-sm text-red-900">{what}</p>
      <p className="text-xs text-red-800">
        Type <code className="font-mono font-semibold">{confirmWord}</code> to confirm.
      </p>
      <input className={inputClass} value={typed} onChange={(e) => setTyped(e.target.value)} />
      <div className="flex gap-2">
        <Button variant="danger" disabled={typed !== confirmWord} onClick={onConfirm}>
          {children}
        </Button>
        <Button variant="secondary" onClick={() => setOpen(false)}>
          Cancel
        </Button>
      </div>
    </div>
  );
}

// A slow operation never shows a bare boolean: state, when it was last checked,
// and whose turn it is are all things a human can act on.
export function CheckedState({
  verified,
  lastCheckedAt,
  waitingOn,
}: {
  verified: boolean;
  lastCheckedAt?: string;
  waitingOn: string;
}) {
  return (
    <span className="inline-flex items-center gap-2">
      {verified ? <Badge tone="ok">verified</Badge> : <Badge tone="warn">waiting on {waitingOn}</Badge>}
      <span className="text-xs text-slate-500">
        {lastCheckedAt ? `last checked ${new Date(lastCheckedAt).toLocaleString()}` : "never checked"}
      </span>
    </span>
  );
}

export function CopyButton({ value, label }: { value: string; label: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <Button
      variant="secondary"
      onClick={() => {
        void navigator.clipboard.writeText(value).then(() => {
          setCopied(true);
          setTimeout(() => setCopied(false), 2000);
        });
      }}
    >
      {copied ? "Copied" : label}
    </Button>
  );
}
