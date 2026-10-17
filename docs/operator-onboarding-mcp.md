# Operator onboarding MCP

The API serves stateless MCP Streamable HTTP at private `/mcp`. Registry
publishes it through the existing Gateway at
`https://mcp.tesserix.app/mcp/onboarding/operator-onboarding-mcp`.
There is no standalone MCP deployment, browser cookie authentication, static
upstream administrator key or token forwarding to another resource.

Use the [Australis operator guide](https://github.com/tesserix/australis/blob/main/docs/guides/operator-onboarding-mcp.md)
for Claude configuration, token refresh, exact roles, tenancy decisions,
onboarding examples, recovery and client contract pins. Its
[ADR-0006](https://github.com/tesserix/australis/blob/main/docs/adr/0006-operator-onboarding-mcp.md)
records the architecture and the explicit operational-write exception to the
grounded assistant's read-only default.

`MCP_GATEWAY_PROJECT_ID` enables the endpoint. The verifier requires both its
Gateway audience and the existing `TOKEN_AUDIENCE`, an access-token client
identifier, and exact project/organization-scoped roles. The initial domain is
Helivanta. Read is required for all tools; create and adopt are independent.
The default Claude client receives read/create only. The endpoint bypasses no
service authorization, operator reconciliation, membership projection or GCS
receipt check. Privileged calls record the dedicated caller's subject in the
existing append-only audit ledger before execution.

Typed Go tools compile their own closed input/output schemas and Registry
resources. Regenerate them from `apps/api`:

```sh
go run ./cmd/mcp-contract > ../../contracts/operator-onboarding-mcp.json
go run ./cmd/mcp-contract --registry > ../../contracts/operator-onboarding-registry.yaml
```

The package tests reject artifact drift. Copy the Registry artifact unchanged
to `tesserix-k8s/charts/apps/agentgateway-route-sync/files/onboarding/registry.yaml`
and the tool catalog to
`australis/internal/adapter/mcp/onboarding-contract.json` in reviewed changes.
The GitOps seed publishes one internal server and five tools through Registry;
route-sync derives the existing Gateway's route from that server. Do not
create a hand-managed HTTPRoute or reduce the global resource prune floor.

Run the full API suite against a disposable PostgreSQL database with
`ONBOARDING_TEST_DSN` set, including `go test -p 1 -race ./...`, vet, build and
lint. The MCP tests cover bearer denial, per-call tool filtering, absent
session IDs, cross-origin denial, private catalog caching, durable same-key
replay, changed-intent conflict, additional identity fields, explicit adoption
authority, preserved login policy and grant-scoped cursor pagination. Existing
service tests continue covering foreign products and operator readiness.

Release through the existing API CI and Kargo path. Enable the endpoint and
publish its Registry contract through `tesserix-k8s`, then verify both API
replicas and route acceptance. Check a retained request non-destructively,
then use a clearly synthetic onboarding request for a full create/replay test.
Save its key and UUID before polling; retain its held receipt. Never delete or
replace a customer as test cleanup. Record live HTTP/JSON-RPC outcomes without
credentials, tokens, patient data or customer binding identifiers in Git.
