import type { Metadata } from "next";
import Link from "next/link";

import "./globals.css";

export const metadata: Metadata = {
  title: "Tesserix onboarding",
  description: "Organizations, product grants, domains and SSO",
};

const nav = [
  { href: "/", label: "Dashboard" },
  { href: "/orgs", label: "Organizations" },
  { href: "/products", label: "Products" },
  { href: "/audit", label: "Audit" },
];

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <body>
        <div className="mx-auto max-w-6xl px-6 py-6">
          <header className="mb-6 flex items-center justify-between border-b border-slate-200 pb-4">
            <div className="flex items-center gap-6">
              <span className="text-sm font-semibold tracking-tight">Tesserix onboarding</span>
              <nav className="flex gap-4">
                {nav.map((item) => (
                  <Link key={item.href} href={item.href} className="text-sm text-slate-600 hover:text-slate-900">
                    {item.label}
                  </Link>
                ))}
              </nav>
            </div>
            <form action="/api/auth/logout" method="post">
              <button className="text-sm text-slate-500 hover:text-slate-900">Sign out</button>
            </form>
          </header>
          {children}
        </div>
      </body>
    </html>
  );
}
