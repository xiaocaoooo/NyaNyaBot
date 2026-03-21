"use client";

import type { ReactNode } from "react";
import { usePathname } from "next/navigation";

import { MainNav } from "@/components/layout/main-nav";
import { useI18n } from "@/components/providers/i18n-provider";

interface AppShellProps {
  children: ReactNode;
}

export function AppShell({ children }: AppShellProps) {
  const pathname = usePathname();
  const { t } = useI18n();
  const hideNav = pathname === "/login" || pathname.startsWith("/login/");

  return (
    <div className="relative min-h-screen">
      <a className="skip-link" href="#content">
        {t("app.skipToContent")}
      </a>
      <div aria-hidden="true" className="pointer-events-none fixed inset-x-0 top-[-160px] z-0 h-[420px]" />
      {!hideNav ? <MainNav /> : null}
      <main className="relative z-10 mx-auto w-full max-w-6xl px-4 py-6 sm:px-6 sm:py-8" id="content">
        {children}
      </main>
    </div>
  );
}
