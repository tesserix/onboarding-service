# Console layout repair

Branch: `fix/console-layout-and-theme`. Status: implemented and locally verified; not deployed.

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

Proposed rollout: only the `onboarding-web` image in the existing `onboarding-service` Argo CD application, namespace `onboarding`, at `https://onboard.tesserix.app`. The API and schema need no migration for this UI change. The currently observed web image tag is `8773c8e133d0435a9bf19e60c4ad5f0d8ad5d061`; retain it as the rollback target. Both current API and web deployments have two ready replicas. Apply the new image through the owning GitOps repository only after the named production rollout is approved. No live onboarding changes have been applied.
