# Opted-in current account name

The existing private customer identity projection accepts optional
`includeTenantName: true` only with `purpose: "profile"`. It adds `tenantName`
only when an active native profile exists and the customer is
currently active and enrolled in Helivanta. The name comes from the owning
customer organization record, uses at most 200 Unicode code points, and is
display metadata rather than authorization. Status, membership and inactive
native profiles disclose no name. A legacy profile may describe a subject with
a different native home organization; the name still identifies the verified
current customer, never that home organization, and is not membership proof.
Normal workload verification,
native/product scope validation, the ten-second deadline and audit-before-
disclosure remain required.

Requests without the flag emit the existing response shape, preserving strict
older decoders. Deploy this owner before consumers opt in; rolling a consumer
back removes its flag and needs no owner rollback or data migration. An invalid
or unavailable name on an eligible opted-in profile fails closed. No new token,
permission, endpoint, datastore or persistent browser record is introduced.

Helivanta forwards the verified name as `tenant_name` on its existing current
profile read and labels its current-account control. It does not infer a name
from an identifier, branding or a branch. Separate native accounts retain their
own destination authentication, membership, roles and MFA.

A separate [assignment-selection purpose](../adr/0007-scoped-assignment-account-selection.md)
is an unreleased candidate for explicitly authorized hospital account selection.
It does not broaden this self-profile read or permit tenant-name disclosure.
