"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { TopNav, TopNavActions, TopNavBrand, TopNavContainer } from "@tesserix/web";

const nav = [
  { href: "/", label: "Dashboard" },
  { href: "/orgs", label: "Organizations" },
  { href: "/products", label: "Products" },
  { href: "/audit", label: "Audit" },
];

export function TopBar() {
  const pathname = usePathname();
  return (
    <TopNav className="border-border/70 bg-background/95">
      <TopNavContainer className="mx-auto h-auto min-h-20 max-w-7xl flex-wrap gap-x-8 gap-y-4 px-5 py-4 sm:px-8">
        <TopNavBrand>
          <Link href="/" className="flex items-center gap-3" aria-label="Tesserix onboarding home">
            <span
              aria-hidden="true"
              className="flex h-10 w-10 items-center justify-center rounded-xl bg-primary text-lg font-semibold text-primary-foreground"
            >
              t.
            </span>
            <span className="flex flex-col gap-0.5">
              <span className="text-base font-semibold tracking-tight">Tesserix</span>
              <span className="text-xs font-normal text-muted-foreground">Onboarding console</span>
            </span>
          </Link>
        </TopNavBrand>
        <nav
          aria-label="Main navigation"
          className="order-3 w-full overflow-x-auto sm:order-none sm:w-auto"
        >
          <ul className="flex items-center gap-1">
            {nav.map((item) => {
              const active =
                item.href === "/"
                  ? pathname === "/"
                  : pathname === item.href || pathname.startsWith(`${item.href}/`);
              return (
                <li key={item.href}>
                  <Link
                    href={item.href}
                    aria-current={active ? "page" : undefined}
                    className={`inline-flex min-h-10 items-center rounded-lg px-4 text-sm font-medium transition-colors focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring ${active ? "bg-secondary text-secondary-foreground" : "text-muted-foreground hover:bg-muted hover:text-foreground"}`}
                  >
                    {item.label}
                  </Link>
                </li>
              );
            })}
          </ul>
        </nav>
        <TopNavActions>
          <form action="/api/auth/logout" method="post">
            <button className="min-h-10 rounded-lg border border-border bg-card px-4 text-sm font-medium text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring">
              Sign out
            </button>
          </form>
        </TopNavActions>
      </TopNavContainer>
    </TopNav>
  );
}
