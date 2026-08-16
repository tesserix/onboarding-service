"use client";

import Link from "next/link";
import { TopNav, TopNavActions, TopNavBrand, TopNavContainer, TopNavLinks } from "@tesserix/web";

const nav = [
  { href: "/", label: "Dashboard" },
  { href: "/orgs", label: "Organizations" },
  { href: "/products", label: "Products" },
  { href: "/audit", label: "Audit" },
];

export function TopBar() {
  return (
    <TopNav>
      <TopNavContainer className="mx-auto max-w-6xl px-6">
        <TopNavBrand>
          <Link href="/" className="font-semibold tracking-tight">
            Tesserix onboarding
          </Link>
        </TopNavBrand>
        <TopNavLinks>
          {nav.map((item) => (
            <li key={item.href}>
              <Link href={item.href} className="text-sm text-muted-foreground transition-colors hover:text-foreground">
                {item.label}
              </Link>
            </li>
          ))}
        </TopNavLinks>
        <TopNavActions>
          <form action="/api/auth/logout" method="post">
            <button className="text-sm text-muted-foreground transition-colors hover:text-foreground">Sign out</button>
          </form>
        </TopNavActions>
      </TopNavContainer>
    </TopNav>
  );
}
