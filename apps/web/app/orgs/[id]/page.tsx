"use client";

import { use, useState } from "react";
import Link from "next/link";

import { AuditTable } from "@/components/AuditTable";
import {
  Badge,
  Button,
  Card,
  CheckedState,
  ConfirmDestructive,
  Empty,
  ErrorNote,
  Field,
  inputClass,
} from "@/components/ui";
import { apiFetch } from "@/lib/api";
import { useApi } from "@/lib/useApi";
import type { AuditEvent, Domain, Grant, IDP, Member, Organization, Product } from "@/lib/types";

const tabs = ["Overview", "Products", "Domains", "Identity providers", "Members", "Audit"] as const;
type Tab = (typeof tabs)[number];

export default function OrgPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = use(params);
  const [tab, setTab] = useState<Tab>("Overview");
  const [error, setError] = useState<string | null>(null);

  const org = useApi<Organization>(`/v1/orgs/${id}`);

  async function act(run: () => Promise<unknown>, reload: () => void) {
    setError(null);
    try {
      await run();
      reload();
    } catch (err) {
      setError((err as Error).message);
    }
  }

  return (
    <div className="space-y-6">
      <ErrorNote error={error ?? org.error} />

      <header className="space-y-5">
        <Link
          href="/orgs"
          className="inline-flex items-center gap-2 text-sm text-muted-foreground hover:text-foreground"
        >
          <span aria-hidden="true">←</span> All organizations
        </Link>
        <div className="flex flex-wrap items-start justify-between gap-4">
          <div className="flex min-w-0 items-start gap-4">
            <span
              aria-hidden="true"
              className="flex h-14 w-14 shrink-0 items-center justify-center rounded-2xl border border-border bg-card text-lg font-semibold text-primary shadow-sm"
            >
              {org.data?.name.slice(0, 2).toUpperCase() || "…"}
            </span>
            <div className="min-w-0">
              <p className="mb-1 text-xs font-medium uppercase tracking-widest text-muted-foreground">
                Organization workspace
              </p>
              <h1 className="text-2xl font-semibold tracking-tight sm:text-3xl">
                {org.data?.name ?? "Loading organization…"}
              </h1>
              <p className="mt-2 break-all text-xs text-muted-foreground">
                Tenant ID <span className="font-mono">{id}</span>
              </p>
            </div>
          </div>
          {org.data && (
            <Badge tone={org.data.state === "active" ? "ok" : "muted"}>{org.data.state}</Badge>
          )}
        </div>
      </header>

      <nav
        aria-label="Organization sections"
        className="flex gap-1 overflow-x-auto border-b border-border pb-3 pt-2"
      >
        {tabs.map((name) => (
          <button
            key={name}
            type="button"
            aria-pressed={tab === name}
            onClick={() => setTab(name)}
            className={`min-h-10 shrink-0 rounded-lg px-4 py-2 text-sm font-medium transition-colors focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring ${
              tab === name
                ? "bg-primary text-primary-foreground shadow-sm"
                : "text-muted-foreground hover:bg-muted hover:text-foreground"
            }`}
          >
            {name}
          </button>
        ))}
      </nav>

      {tab === "Overview" && org.data && (
        <Overview org={org.data} onAct={(run) => act(run, org.reload)} />
      )}
      {tab === "Products" && <Products orgId={id} onAct={act} />}
      {tab === "Domains" && <Domains orgId={id} onAct={act} />}
      {tab === "Identity providers" && <IDPs orgId={id} onAct={act} />}
      {tab === "Members" && <Members orgId={id} onAct={act} />}
      {tab === "Audit" && <Audit orgId={id} />}
    </div>
  );
}

type Act = (run: () => Promise<unknown>, reload: () => void) => void;

function Overview({
  org,
  onAct,
}: {
  org: Organization;
  onAct: (run: () => Promise<unknown>) => void;
}) {
  const fields: [string, string | number | undefined][] = [
    ["Identity org", org.zitadelOrgId],
    ["Primary domain", org.primaryDomain],
    ["Onboarded by", org.createdByProduct || "platform"],
    ["Billing email", org.billingEmail],
    ["Support email", org.supportEmail],
    ["Plan", org.plan],
    ["Seats", org.seats],
    ["Region", org.region],
    ["External reference", org.externalRef],
    ["Created", new Date(org.createdAt).toLocaleString()],
  ];
  const inactive = org.state !== "active";

  return (
    <div className="space-y-4">
      <Card title="Details">
        <dl className="grid grid-cols-1 gap-2 text-sm sm:grid-cols-2">
          {fields.map(([label, value]) => (
            <div key={label} className="flex justify-between border-b border-border py-1">
              <dt className="text-muted-foreground">{label}</dt>
              <dd className="font-mono text-xs">
                {value === undefined || value === "" ? "—" : value}
              </dd>
            </div>
          ))}
        </dl>
        {org.labels && Object.keys(org.labels).length > 0 && (
          <div className="mt-3 flex flex-wrap gap-2">
            {Object.entries(org.labels).map(([key, value]) => (
              <Badge key={key} tone="muted">
                {key}={value}
              </Badge>
            ))}
          </div>
        )}
      </Card>

      <Card title="State">
        {inactive ? (
          <Button
            onClick={() =>
              onAct(() => apiFetch(`/v1/orgs/${org.id}/reactivate`, { method: "POST" }))
            }
          >
            Reactivate
          </Button>
        ) : (
          <ConfirmDestructive
            what={`Deactivating ${org.name} signs out every one of its users across every product.`}
            confirmWord={org.name}
            onConfirm={() =>
              onAct(() => apiFetch(`/v1/orgs/${org.id}/deactivate`, { method: "POST" }))
            }
          >
            Deactivate
          </ConfirmDestructive>
        )}
      </Card>
    </div>
  );
}

function Products({ orgId, onAct }: { orgId: string; onAct: Act }) {
  const grants = useApi<{ grants: Grant[] }>(`/v1/orgs/${orgId}/grants`);
  const products = useApi<{ products: Product[] }>("/v1/products");
  const [productKey, setProductKey] = useState("");

  const held = new Set(
    (grants.data?.grants ?? []).filter((g) => !g.revokedAt).map((g) => g.productKey),
  );
  const available = (products.data?.products ?? []).filter(
    (p) => p.identity === "zitadel" && !held.has(p.key),
  );

  return (
    <div className="space-y-4">
      <Card title="Grants">
        {grants.data?.grants.length ? (
          <ul className="divide-y divide-border">
            {grants.data.grants.map((grant) => (
              <li key={grant.productKey} className="flex items-center justify-between py-2 text-sm">
                <div>
                  <span className="font-medium">{grant.productKey}</span>
                  <span className="ml-2 font-mono text-xs text-muted-foreground">
                    {grant.roles.join(", ")}
                  </span>
                </div>
                {grant.revokedAt ? (
                  <Badge tone="muted">revoked</Badge>
                ) : (
                  <ConfirmDestructive
                    what={`Revoking ${grant.productKey} removes access for this organization's entire staff.`}
                    confirmWord={grant.productKey}
                    onConfirm={() =>
                      onAct(
                        () =>
                          apiFetch(`/v1/orgs/${orgId}/grants/${grant.productKey}`, {
                            method: "DELETE",
                          }),
                        grants.reload,
                      )
                    }
                  >
                    Revoke
                  </ConfirmDestructive>
                )}
              </li>
            ))}
          </ul>
        ) : (
          <Empty>No products granted.</Empty>
        )}
      </Card>

      <Card title="Grant a product">
        <div className="flex items-end gap-2">
          <Field label="Product">
            <select
              className={inputClass}
              value={productKey}
              onChange={(e) => setProductKey(e.target.value)}
            >
              <option value="">Choose…</option>
              {available.map((p) => (
                <option key={p.key} value={p.key}>
                  {p.displayName}
                </option>
              ))}
            </select>
          </Field>
          <Button
            disabled={!productKey}
            onClick={() =>
              onAct(
                () =>
                  apiFetch(`/v1/orgs/${orgId}/grants`, { method: "POST", body: { productKey } }),
                grants.reload,
              )
            }
          >
            Grant
          </Button>
        </div>
      </Card>
    </div>
  );
}

function Domains({ orgId, onAct }: { orgId: string; onAct: Act }) {
  const domains = useApi<{ domains: Domain[] }>(`/v1/orgs/${orgId}/domains`);
  const [domain, setDomain] = useState("");

  return (
    <div className="space-y-4">
      <Card title="Domains">
        {domains.data?.domains.length ? (
          <ul className="divide-y divide-border">
            {domains.data.domains.map((d) => (
              <li key={d.domain} className="space-y-2 py-3 text-sm">
                <div className="flex items-center justify-between">
                  <span className="font-medium">{d.domain}</span>
                  <CheckedState
                    verified={d.state === "verified"}
                    lastCheckedAt={d.lastCheckedAt}
                    waitingOn="the customer's DNS"
                  />
                </div>
                {d.challengeValue && d.state !== "verified" && (
                  <pre className="overflow-x-auto rounded-md bg-foreground p-2 text-xs text-background">
                    {d.challengeType} {d.domain} {d.challengeValue}
                  </pre>
                )}
                <div className="flex gap-2">
                  {d.state !== "verified" && (
                    <Button
                      variant="secondary"
                      onClick={() =>
                        onAct(
                          () =>
                            apiFetch(
                              `/v1/orgs/${orgId}/domains/${encodeURIComponent(d.domain)}/verify`,
                              {
                                method: "POST",
                              },
                            ),
                          domains.reload,
                        )
                      }
                    >
                      Check now
                    </Button>
                  )}
                  <ConfirmDestructive
                    what={`Removing ${d.domain} stops email discovery routing its users to this organization.`}
                    confirmWord={d.domain}
                    onConfirm={() =>
                      onAct(
                        () =>
                          apiFetch(`/v1/orgs/${orgId}/domains/${encodeURIComponent(d.domain)}`, {
                            method: "DELETE",
                          }),
                        domains.reload,
                      )
                    }
                  >
                    Remove
                  </ConfirmDestructive>
                </div>
              </li>
            ))}
          </ul>
        ) : (
          <Empty>No domains claimed.</Empty>
        )}
      </Card>

      <Card title="Claim a domain">
        <div className="flex items-end gap-2">
          <Field label="Domain" hint="Discovery only routes to it once the DNS record is in place.">
            <input
              className={inputClass}
              value={domain}
              onChange={(e) => setDomain(e.target.value)}
            />
          </Field>
          <Button
            disabled={!domain}
            onClick={() =>
              onAct(
                () => apiFetch(`/v1/orgs/${orgId}/domains`, { method: "POST", body: { domain } }),
                () => {
                  setDomain("");
                  domains.reload();
                },
              )
            }
          >
            Claim
          </Button>
        </div>
      </Card>
    </div>
  );
}

const idpTypes = ["oidc", "okta", "entra", "google", "github", "saml", "ldap"] as const;

function IDPs({ orgId, onAct }: { orgId: string; onAct: Act }) {
  const idps = useApi<{ idps: IDP[] }>(`/v1/orgs/${orgId}/idps`);
  const [form, setForm] = useState({
    type: "oidc",
    name: "",
    issuer: "",
    clientId: "",
    clientSecret: "",
    tenantId: "",
    metadataXml: "",
    autoRegister: true,
  });

  const saml = form.type === "saml";

  return (
    <div className="space-y-4">
      <Card title="Identity providers">
        {idps.data?.idps.length ? (
          <ul className="divide-y divide-border">
            {idps.data.idps.map((idp) => (
              <li key={idp.id} className="flex items-center justify-between py-2 text-sm">
                <div>
                  <span className="font-medium">{idp.displayName}</span>
                  <span className="ml-2 font-mono text-xs text-muted-foreground">{idp.type}</span>
                  {idp.autoRegister && (
                    <span className="ml-2 text-xs text-muted-foreground">
                      auto-registers new users
                    </span>
                  )}
                </div>
                <ConfirmDestructive
                  what={`Deleting ${idp.displayName} locks out everyone who signs in through it.`}
                  confirmWord={idp.displayName}
                  onConfirm={() =>
                    onAct(
                      () => apiFetch(`/v1/orgs/${orgId}/idps/${idp.id}`, { method: "DELETE" }),
                      idps.reload,
                    )
                  }
                >
                  Delete
                </ConfirmDestructive>
              </li>
            ))}
          </ul>
        ) : (
          <Empty>No identity providers. Users sign in with a password.</Empty>
        )}
      </Card>

      {/* The client secret is forwarded to Zitadel and dropped — this console
          never stores it, and it is never read back. */}
      <Card title="Connect an identity provider">
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <Field label="Type">
            <select
              className={inputClass}
              value={form.type}
              onChange={(e) => setForm({ ...form, type: e.target.value })}
            >
              {idpTypes.map((type) => (
                <option key={type} value={type}>
                  {type}
                </option>
              ))}
            </select>
          </Field>
          <Field label="Display name" hint="What the customer's staff see on the sign-in button.">
            <input
              className={inputClass}
              value={form.name}
              onChange={(e) => setForm({ ...form, name: e.target.value })}
            />
          </Field>
          {!saml && (
            <>
              <Field label="Issuer">
                <input
                  className={inputClass}
                  value={form.issuer}
                  onChange={(e) => setForm({ ...form, issuer: e.target.value })}
                />
              </Field>
              <Field label="Client id">
                <input
                  className={inputClass}
                  value={form.clientId}
                  onChange={(e) => setForm({ ...form, clientId: e.target.value })}
                />
              </Field>
              <Field
                label="Client secret"
                hint="Write-only. It goes straight to the identity plane."
              >
                <input
                  className={inputClass}
                  type="password"
                  autoComplete="off"
                  value={form.clientSecret}
                  onChange={(e) => setForm({ ...form, clientSecret: e.target.value })}
                />
              </Field>
              <Field label="Directory tenant id" hint="Entra only.">
                <input
                  className={inputClass}
                  value={form.tenantId}
                  onChange={(e) => setForm({ ...form, tenantId: e.target.value })}
                />
              </Field>
            </>
          )}
          {saml && (
            <Field label="Metadata XML">
              <textarea
                className={`${inputClass} h-32 font-mono text-xs`}
                value={form.metadataXml}
                onChange={(e) => setForm({ ...form, metadataXml: e.target.value })}
              />
            </Field>
          )}
          <label className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={form.autoRegister}
              onChange={(e) => setForm({ ...form, autoRegister: e.target.checked })}
            />
            Create an account on first sign-in
          </label>
        </div>
        <div className="mt-4">
          <Button
            disabled={!form.name}
            onClick={() =>
              onAct(
                () =>
                  apiFetch(`/v1/orgs/${orgId}/idps`, {
                    method: "POST",
                    body: {
                      type: form.type,
                      name: form.name,
                      issuer: form.issuer || undefined,
                      clientId: form.clientId || undefined,
                      clientSecret: form.clientSecret || undefined,
                      tenantId: form.tenantId || undefined,
                      metadataXml: form.metadataXml || undefined,
                      autoRegister: form.autoRegister,
                    },
                  }),
                () => {
                  setForm({ ...form, clientSecret: "", metadataXml: "" });
                  idps.reload();
                },
              )
            }
          >
            Connect
          </Button>
        </div>
      </Card>
    </div>
  );
}

function Members({ orgId, onAct }: { orgId: string; onAct: Act }) {
  const members = useApi<{ members: Member[] }>(`/v1/orgs/${orgId}/members`);
  const [form, setForm] = useState({ email: "", name: "", role: "ORG_USER_MANAGER" });

  return (
    <div className="grid items-start gap-6 lg:grid-cols-[minmax(0,1.5fr)_minmax(320px,1fr)]">
      <Card
        title="Members"
        description="People and service identities with access to this organization."
        action={members.data && <Badge tone="muted">{members.data.members.length} members</Badge>}
      >
        {members.data?.members.length ? (
          <ul className="divide-y divide-border">
            {members.data.members.map((member) => (
              <li
                key={member.userId}
                className="flex flex-wrap items-center justify-between gap-4 py-5 text-sm first:pt-0 last:pb-0"
              >
                <div className="flex min-w-0 items-start gap-3">
                  <span
                    aria-hidden="true"
                    className="flex h-10 w-10 shrink-0 items-center justify-center rounded-full bg-secondary text-sm font-semibold text-secondary-foreground"
                  >
                    {(member.displayName || member.email || "?").slice(0, 2).toUpperCase()}
                  </span>
                  <div className="min-w-0">
                    <p className="break-all font-medium leading-5">
                      {member.displayName || member.email || member.userId}
                    </p>
                    {member.displayName && member.email && (
                      <p className="mt-1 break-all text-xs text-muted-foreground">{member.email}</p>
                    )}
                    <div className="mt-2 flex flex-wrap gap-1.5">
                      {member.roles.map((role) => (
                        <Badge key={role} tone="muted">
                          {(
                            {
                              ORG_OWNER: "Owner",
                              ORG_USER_MANAGER: "User manager",
                              ORG_OWNER_VIEWER: "Viewer",
                            } as Record<string, string>
                          )[role] ?? role}
                        </Badge>
                      ))}
                    </div>
                  </div>
                </div>
                <ConfirmDestructive
                  what={`Removing ${member.email || member.userId} ends their access to this organization.`}
                  confirmWord={member.email || member.userId}
                  onConfirm={() =>
                    onAct(
                      () =>
                        apiFetch(`/v1/orgs/${orgId}/members/${member.userId}`, {
                          method: "DELETE",
                        }),
                      members.reload,
                    )
                  }
                >
                  Remove
                </ConfirmDestructive>
              </li>
            ))}
          </ul>
        ) : (
          <Empty>No members.</Empty>
        )}
      </Card>

      <Card
        title="Invite a member"
        description="Invite a colleague and choose the access they need."
      >
        <div className="grid grid-cols-1 gap-5">
          <Field label="Work email">
            <input
              className={inputClass}
              type="email"
              autoComplete="email"
              placeholder="name@organization.com"
              value={form.email}
              onChange={(e) => setForm({ ...form, email: e.target.value })}
            />
          </Field>
          <Field label="Name" hint="Optional. Their name in the member directory.">
            <input
              className={inputClass}
              value={form.name}
              onChange={(e) => setForm({ ...form, name: e.target.value })}
            />
          </Field>
          <Field label="Role" hint="Choose only the access this person needs.">
            <select
              className={inputClass}
              value={form.role}
              onChange={(e) => setForm({ ...form, role: e.target.value })}
            >
              <option value="ORG_OWNER">Owner</option>
              <option value="ORG_USER_MANAGER">User manager</option>
              <option value="ORG_OWNER_VIEWER">Viewer</option>
            </select>
          </Field>
        </div>
        <div className="mt-6 flex justify-end border-t border-border pt-5">
          <Button
            disabled={!form.email}
            onClick={() =>
              onAct(
                () =>
                  apiFetch(`/v1/orgs/${orgId}/members`, {
                    method: "POST",
                    body: { email: form.email, name: form.name || undefined, role: form.role },
                  }),
                () => {
                  setForm({ ...form, email: "", name: "" });
                  members.reload();
                },
              )
            }
          >
            Invite
          </Button>
        </div>
      </Card>
    </div>
  );
}

function Audit({ orgId }: { orgId: string }) {
  const audit = useApi<{ events: AuditEvent[] }>(`/v1/orgs/${orgId}/audit?limit=200`);
  return (
    <Card title="Audit">
      <AuditTable events={audit.data?.events ?? []} />
    </Card>
  );
}
