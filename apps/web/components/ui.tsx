"use client";

import { useState, type ReactNode } from "react";
import {
  Alert,
  AlertDescription,
  Badge as DsBadge,
  Button as DsButton,
  Card as DsCard,
  CardAction,
  CardContent,
  CardHeader,
  CardTitle,
  EmptyState,
  EmptyStateDescription,
  Input,
  Label,
  PageHeader,
  PageHeaderActions,
  PageHeaderDescription,
  PageHeaderHeading,
  PageHeaderTitle,
  PageHeaderTop,
} from "@tesserix/web";

export function PageTitle({
  title,
  description,
  actions,
}: {
  title: string;
  description?: string;
  actions?: ReactNode;
}) {
  return (
    <PageHeader>
      <PageHeaderTop>
        <PageHeaderHeading>
          <PageHeaderTitle>{title}</PageHeaderTitle>
          {description && <PageHeaderDescription>{description}</PageHeaderDescription>}
        </PageHeaderHeading>
        {actions && <PageHeaderActions>{actions}</PageHeaderActions>}
      </PageHeaderTop>
    </PageHeader>
  );
}

export function Card({
  title,
  description,
  action,
  children,
}: {
  title?: string;
  description?: string;
  action?: ReactNode;
  children: ReactNode;
}) {
  return (
    <DsCard className="rounded-2xl border-border/70 shadow-sm hover:shadow-sm">
      {(title || action) && (
        <CardHeader className="gap-2 pb-5">
          <CardTitle className="text-base font-semibold leading-6">{title}</CardTitle>
          {description && (
            <p className="text-sm leading-relaxed text-muted-foreground">{description}</p>
          )}
          {action && <CardAction>{action}</CardAction>}
        </CardHeader>
      )}
      <CardContent>{children}</CardContent>
    </DsCard>
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
  const dsVariant = { primary: "default", secondary: "outline", danger: "destructive" } as const;
  return (
    <DsButton
      type={type}
      onClick={onClick}
      disabled={disabled}
      size="sm"
      variant={dsVariant[variant]}
    >
      {children}
    </DsButton>
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
    <Label className="flex min-w-0 flex-col items-stretch gap-2 text-sm font-medium">
      <span>{label}</span>
      {children}
      {hint && <span className="block text-xs font-normal text-muted-foreground">{hint}</span>}
    </Label>
  );
}

// Pages render bare <input>/<select>/<textarea>, so the design system's field
// styling is shared as a class rather than as a component.
export const inputClass =
  "flex h-11 w-full min-w-0 rounded-lg border border-input bg-background px-3 py-2 text-sm font-normal shadow-xs outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 disabled:opacity-50";

export function Badge({
  tone,
  children,
}: {
  tone: "ok" | "warn" | "bad" | "muted";
  children: ReactNode;
}) {
  const variant = { ok: "success", warn: "warning", bad: "destructive", muted: "neutral" } as const;
  return <DsBadge variant={variant[tone]}>{children}</DsBadge>;
}

export function ErrorNote({ error }: { error: string | null }) {
  if (!error) return null;
  return (
    <Alert role="alert" variant="destructive">
      <AlertDescription>{error}</AlertDescription>
    </Alert>
  );
}

// The org exists but something after it did not. Not an error — the operator
// still has to act on it.
export function WarningNote({ warnings }: { warnings: string[] }) {
  if (warnings.length === 0) return null;
  return (
    <Alert role="status" variant="warning">
      <AlertDescription>
        <ul className="list-disc space-y-1 pl-4">
          {warnings.map((w) => (
            <li key={w}>{w}</li>
          ))}
        </ul>
      </AlertDescription>
    </Alert>
  );
}

export function Empty({ children }: { children: ReactNode }) {
  return (
    <EmptyState size="sm">
      <EmptyStateDescription>{children}</EmptyStateDescription>
    </EmptyState>
  );
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
      <Button variant="secondary" onClick={() => setOpen(true)}>
        {children}
      </Button>
    );
  }
  return (
    <div className="space-y-3 rounded-md border border-destructive/30 bg-destructive/5 p-3">
      <p className="text-sm">{what}</p>
      <p className="text-xs text-muted-foreground">
        Type <code className="font-mono font-semibold">{confirmWord}</code> to confirm.
      </p>
      <Input value={typed} onChange={(e) => setTyped(e.target.value)} />
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
      {verified ? (
        <Badge tone="ok">verified</Badge>
      ) : (
        <Badge tone="warn">waiting on {waitingOn}</Badge>
      )}
      <span className="text-xs text-muted-foreground">
        {lastCheckedAt
          ? `last checked ${new Date(lastCheckedAt).toLocaleString()}`
          : "never checked"}
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
