"use client";

import { use, useState } from "react";

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

      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-semibold">{org.data?.name ?? "…"}</h1>
          <p className="font-mono text-xs text-slate-500">tenant {id}</p>
        </div>
        {org.data && <Badge tone={org.data.state === "active" ? "ok" : "muted"}>{org.data.state}</Badge>}
      </div>

      <nav className="flex gap-2 border-b border-slate-200 pb-2">
        {tabs.map((name) => (
          <button
            key={name}
            onClick={() => setTab(name)}
            className={`rounded-md px-3 py-1 text-sm ${
              tab === name ? "bg-slate-900 text-white" : "text-slate-600 hover:bg-slate-100"
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

function Overview({ org, onAct }: { org: Organization; onAct: (run: () => Promise<unknown>) => void }) {
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
            <div key={label} className="flex justify-between border-b border-slate-100 py-1">
              <dt className="text-slate-500">{label}</dt>
              <dd className="font-mono text-xs">{value === undefined || value === "" ? "—" : value}</dd>
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
          <Button onClick={() => onAct(() => apiFetch(`/v1/orgs/${org.id}/reactivate`, { method: "POST" }))}>
            Reactivate
          </Button>
        ) : (
          <ConfirmDestructive
            what={`Deactivating ${org.name} signs out every one of its users across every product.`}
            confirmWord={org.name}
            onConfirm={() => onAct(() => apiFetch(`/v1/orgs/${org.id}/deactivate`, { method: "POST" }))}
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

  const held = new Set((grants.data?.grants ?? []).filter((g) => !g.revokedAt).map((g) => g.productKey));
  const available = (products.data?.products ?? []).filter((p) => p.identity === "zitadel" && !held.has(p.key));

  return (
    <div className="space-y-4">
      <Card title="Grants">
        {grants.data?.grants.length ? (
          <ul className="divide-y divide-slate-100">
            {grants.data.grants.map((grant) => (
              <li key={grant.productKey} className="flex items-center justify-between py-2 text-sm">
                <div>
                  <span className="font-medium">{grant.productKey}</span>
                  <span className="ml-2 font-mono text-xs text-slate-500">{grant.roles.join(", ")}</span>
                </div>
                {grant.revokedAt ? (
                  <Badge tone="muted">revoked</Badge>
                ) : (
                  <ConfirmDestructive
                    what={`Revoking ${grant.productKey} removes access for this organization's entire staff.`}
                    confirmWord={grant.productKey}
                    onConfirm={() =>
                      onAct(
                        () => apiFetch(`/v1/orgs/${orgId}/grants/${grant.productKey}`, { method: "DELETE" }),
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
            <select className={inputClass} value={productKey} onChange={(e) => setProductKey(e.target.value)}>
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
                () => apiFetch(`/v1/orgs/${orgId}/grants`, { method: "POST", body: { productKey } }),
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
          <ul className="divide-y divide-slate-100">
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
                  <pre className="overflow-x-auto rounded-md bg-slate-900 p-2 text-xs text-slate-100">
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
                            apiFetch(`/v1/orgs/${orgId}/domains/${encodeURIComponent(d.domain)}/verify`, {
                              method: "POST",
                            }),
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
            <input className={inputClass} value={domain} onChange={(e) => setDomain(e.target.value)} />
          </Field>
          <Button
            disabled={!domain}
            onClick={() =>
              onAct(() => apiFetch(`/v1/orgs/${orgId}/domains`, { method: "POST", body: { domain } }), () => {
                setDomain("");
                domains.reload();
              })
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
          <ul className="divide-y divide-slate-100">
            {idps.data.idps.map((idp) => (
              <li key={idp.id} className="flex items-center justify-between py-2 text-sm">
                <div>
                  <span className="font-medium">{idp.displayName}</span>
                  <span className="ml-2 font-mono text-xs text-slate-500">{idp.type}</span>
                  {idp.autoRegister && (
                    <span className="ml-2 text-xs text-slate-500">auto-registers new users</span>
                  )}
                </div>
                <ConfirmDestructive
                  what={`Deleting ${idp.displayName} locks out everyone who signs in through it.`}
                  confirmWord={idp.displayName}
                  onConfirm={() =>
                    onAct(() => apiFetch(`/v1/orgs/${orgId}/idps/${idp.id}`, { method: "DELETE" }), idps.reload)
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
            <input className={inputClass} value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} />
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
              <Field label="Client secret" hint="Write-only. It goes straight to the identity plane.">
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
    <div className="space-y-4">
      <Card title="Members">
        {members.data?.members.length ? (
          <ul className="divide-y divide-slate-100">
            {members.data.members.map((member) => (
              <li key={member.userId} className="flex items-center justify-between py-2 text-sm">
                <div>
                  <span className="font-medium">{member.displayName || member.email || member.userId}</span>
                  <span className="ml-2 font-mono text-xs text-slate-500">{member.roles.join(", ")}</span>
                </div>
                <ConfirmDestructive
                  what={`Removing ${member.email || member.userId} ends their access to this organization.`}
                  confirmWord={member.email || member.userId}
                  onConfirm={() =>
                    onAct(
                      () => apiFetch(`/v1/orgs/${orgId}/members/${member.userId}`, { method: "DELETE" }),
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

      <Card title="Invite a member">
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
          <Field label="Email">
            <input
              className={inputClass}
              type="email"
              value={form.email}
              onChange={(e) => setForm({ ...form, email: e.target.value })}
            />
          </Field>
          <Field label="Name">
            <input
              className={inputClass}
              value={form.name}
              onChange={(e) => setForm({ ...form, name: e.target.value })}
            />
          </Field>
          <Field label="Role">
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
        <div className="mt-4">
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
