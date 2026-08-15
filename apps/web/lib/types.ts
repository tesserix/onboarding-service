export type Principal = {
  kind: "platform" | "orgAdmin" | "product";
  subject: string;
  orgId?: string;
  productKey?: string;
  roles?: string[];
};

export type Product = {
  key: string;
  displayName: string;
  zitadelProjectId: string;
  oidcClientId?: string;
  hosts: string[];
  identity: "zitadel" | "gip";
  tenantSecrets: "openbao" | "none";
  tenancy: "org" | "ephemeral+org";
  roles: string[];
  dataResidency: string;
  state: string;
  createdAt: string;
};

export type Organization = {
  id: string;
  zitadelOrgId: string;
  name: string;
  primaryDomain?: string;
  state: "active" | "inactive";
  createdByProduct?: string;
  billingEmail?: string;
  supportEmail?: string;
  plan?: string;
  seats?: number;
  region?: string;
  externalRef?: string;
  labels?: Record<string, string>;
  createdAt: string;
};

export type Grant = {
  orgId: string;
  productKey: string;
  roles: string[];
  grantedAt: string;
  revokedAt?: string;
};

export type Domain = {
  orgId: string;
  domain: string;
  state: string;
  challengeType?: string;
  challengeValue?: string;
  lastCheckedAt?: string;
  verifiedAt?: string;
};

export type IDP = {
  orgId: string;
  id: string;
  type: string;
  displayName: string;
  autoRegister: boolean;
  state: string;
  createdAt: string;
};

export type Member = {
  userId: string;
  email?: string;
  displayName?: string;
  roles: string[];
};

export type AuditEvent = {
  seq: number;
  occurredAt: string;
  actorType: string;
  actorId: string;
  productKey?: string;
  orgId?: string;
  action: string;
  target?: string;
  outcome: "success" | "denied" | "error";
  requestId: string;
  detail?: string;
};
