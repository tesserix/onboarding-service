# Console layout repair

Layout implementation: PR #8 merged. Release preparation: PR #9. Status: locally verified; not deployed.

The organisation detail screenshot exposed an unselected design-system theme, missing package source registration for Tailwind, and crowded navigation/form layout. The document now selects the installed `default` theme and Tailwind scans the published `@tesserix/web/dist` files. Shared success, warning and neutral badge tokens are registered explicitly because this package version uses those utilities without declaring the corresponding theme colours.

The console now has a labelled, responsive navigation bar with an active section; a breadcrumb and organisation header; a selected organisation section; lighter cards; readable member names and role badges; and a separate invitation panel that stacks beneath the directory at smaller widths. Destructive operations retain their typed confirmation. This change does not invite, remove or grant access to any member.

Validation also reproduced an existing request race: a previous organisation response could replace the current route's data. The query hook now associates results with their request path and refresh version and ignores responses after effect cleanup. It keeps current data while refreshing the same path and clears it immediately when the path changes.

The obsolete `next lint` command was replaced with ESLint's Next.js 16 configuration, and CI runs it. The OAuth login redirect keeps full-document navigation because it targets an API redirect chain, not an internal client route.

Verification commands (from `apps/web`):

```sh
pnpm lint
pnpm typecheck
pnpm test
pnpm build
```

The theme and compiled CSS tests, active navigation test, and organisation request race tests were observed failing before the fix. Browser review used the production build behind a local read-only fixture server with synthetic YES HOSPITAL data. Desktop layout was visually inspected. At a 390px viewport, document content width was also 390px; the directory and invite form stack, and navigation scrolls inside its own container. Local fixtures reject all mutations and are not production API evidence.

The approved rollout now follows [the standard delivery contract](adr/0001-standard-delivery.md): shared reusable workflows and digest-pinned base images, followed by a Kargo promotion of API and web together. The original web-only manual image pin was superseded by this requirement. No schema migration is included.

The release checks also found and patched vulnerable dependencies: Next.js 16.4.0,
Sharp 0.35.5, Go 1.26.9 and affected Go networking/crypto/text libraries. Tailwind
remains version 4. Both Linux images built from shared Tesserix bases pass Trivy's
fixable HIGH/CRITICAL gate. Govulncheck reports no reachable vulnerabilities; one
advisory remains only in an unused required module. Full-history Gitleaks passes.

The 21 web tests, format/lint/type checks, production build, real-PostgreSQL Go race
tests, vet/build and release-contract tests pass locally. The exact CI schema
bootstrap was exercised in a disposable database with its checksum verified.
Coverage with PostgreSQL is 47.4%; this is a regression baseline, not comprehensive
coverage. Local browser checks use the built image with synthetic read-only API
fixtures and therefore do not establish production API write behaviour.

GitHub Actions accepted the reusable workflow calls, but private-repository jobs
were blocked before executing by account billing/spending limits. The shared
workflow PR additionally needs the independently required GitHub review. Production
cutover and live browser/API verification remain pending those gates.

The currently deployed API and web image tag is
`8773c8e133d0435a9bf19e60c4ad5f0d8ad5d061`, with two ready replicas each; retain it as
the pre-cutover recovery target. No live onboarding changes have been applied.
