import type { Metadata } from "next";

import { TopBar } from "@/components/TopBar";

import "./globals.css";

export const metadata: Metadata = {
  title: "Tesserix onboarding",
  description: "Organizations, product grants, domains and SSO",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en" data-theme="default">
      <body className="min-h-screen bg-muted/40">
        <TopBar />
        <main className="mx-auto w-full max-w-7xl px-5 py-8 sm:px-8 sm:py-10">{children}</main>
      </body>
    </html>
  );
}
