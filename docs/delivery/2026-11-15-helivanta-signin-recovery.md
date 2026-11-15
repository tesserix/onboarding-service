# Helivanta existing-owner sign-in recovery

The acceptance target is the existing Yes Hospital tenant and its approved owner.
Preserve the tenant, native identity, credentials, ownership and data. No migration,
credential rotation, new permission grant or replacement tenant is part of this fix.

The observed browser failure occurred during account discovery before password
entry. Read-only owner-source checks found one active verified native account;
organisation/tenant metadata, project ownership, live exact grant, revisions and
the onboarding store's active enrolment all agreed. Those observations do not
prove the complete browser sign-in path. Diagnostic requests from outside the
cluster observed variable source latency, including long metadata reads; they
are not production SLO measurements or proof of a sole root cause.

The verifier had a scheduling barrier: grant point lookup waited for all four
initial reads, although only grant search supplies its input. A deterministic
protocol regression makes an independent organisation read await the point
lookup; the old implementation exhausts its unchanged 750 ms deadline. The new
implementation runs that search/point chain alongside the three independent
reads. It retains exact scope and owner checks before the point call, waits for
all sources before publication, checks the latest grant revision against search,
and preserves missing/unavailable error precedence. No cache, retry, timeout
increase, weaker ownership rule or partial-source success is introduced.

The same test also returns a late organisation 503 after a successful point read;
identity remains unavailable. Existing protocol tests cover inactive, missing,
deleted, foreign, malformed, oversized and redirected sources and cancellation.
The deadline and wire schemas are unchanged.

Account lookup failures now emit only fixed phase, elapsed milliseconds and
parent-context cancellation/deadline flags. Proof, email, user/org IDs, raw
upstream errors, response bodies, tokens, cookies, codes and passwords are not
log attributes. A pure regression checks unavailable dependencies and a wrapped
private upstream error for proof/secret leakage. Public error bodies remain the
same. These phases distinguish proof lookup, identity discovery, canonical store
reads, live source verification, response conformance and audit failures.

## Release acceptance still required

Run the full owning CI, signed-image and standard GitOps/Kargo release. Confirm
exact image digest and healthy replicas, then repeat normal email discovery,
account selection, password and MFA sign-in on the selected existing tenant.
Use live fixed-phase diagnostics if any failure remains. The previous challenge
has a ten-minute expiry; a fresh challenge after release is a sign-in proof,
not a reset of already-verified identity email. Password entry uses only the
approved local secret file through the normal sign-in destination. No live
browser success or complete recovery is claimed before that test.
