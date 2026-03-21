"use client";

import { motion } from "framer-motion";
import Link from "next/link";
import { usePathname } from "next/navigation";

import { useI18n } from "@/components/providers/i18n-provider";
import { ThemeToggle } from "@/components/theme/theme-toggle";
import { cn } from "@/lib/utils/cn";

const navItems = [
  { href: "/", labelKey: "nav.dashboard" },
  { href: "/plugins", labelKey: "nav.plugins" },
  { href: "/config", labelKey: "nav.config" },
] as const;

function normalizePathname(pathname: string): string {
  if (!pathname) {
    return "/";
  }
  if (pathname === "/") {
    return pathname;
  }
  return pathname.endsWith("/") ? pathname.slice(0, -1) : pathname;
}

function isNavActive(itemHref: string, currentPathname: string): boolean {
  const current = normalizePathname(currentPathname);
  const item = normalizePathname(itemHref);

  if (item === "/") {
    return current === "/";
  }
  if (item === "/plugins") {
    return current === item || current.startsWith("/plugins/");
  }
  if (item === "/config") {
    return current === item || current.startsWith("/config/") || current === "/globals" || current.startsWith("/globals/");
  }
  return current === item;
}

export function MainNav() {
  const pathname = usePathname();
  const { t } = useI18n();

  return (
    <header className="sticky top-0 z-40 border-b border-border/70 bg-surface/85 backdrop-blur-lg">
      <div className="mx-auto flex w-full max-w-6xl items-center justify-between gap-4 px-4 py-3 sm:px-6">
        <div className="flex items-center gap-3">
          <div
            aria-hidden="true"
            className="h-8 w-8 rounded-lg bg-gradient-to-br from-primary/80 to-primary/50 shadow-[0_8px_20px_rgba(2,132,199,0.25)]"
          />
          <div>
            <p className="text-sm font-semibold tracking-wide text-text">NyaNyaBot</p>
            <p className="text-xs text-muted">{t("nav.subtitle")}</p>
          </div>
        </div>

        <nav aria-label={t("nav.mainAria")} className="flex items-center gap-1 rounded-xl border border-border/70 bg-surface-elevated/80 p-1">
          {navItems.map((item) => {
            const active = isNavActive(item.href, pathname);

            return (
              <Link
                key={item.href}
                aria-current={active ? "page" : undefined}
                className={cn(
                  "relative isolate rounded-lg px-3 py-1.5 text-sm font-medium transition",
                  "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary focus-visible:ring-offset-2 focus-visible:ring-offset-bg",
                  active ? "text-primary-foreground" : "text-muted hover:bg-surface hover:text-text",
                )}
                href={item.href}
              >
                {active ? (
                  <motion.span
                    aria-hidden="true"
                    className="absolute inset-0 -z-10 rounded-lg bg-primary shadow-[0_8px_18px_rgba(2,132,199,0.28)]"
                    layoutId="main-nav-slider"
                    transition={{ bounce: 0.2, duration: 0.24 }}
                  />
                ) : null}
                {t(item.labelKey)}
              </Link>
            );
          })}
        </nav>

        <ThemeToggle />
      </div>
    </header>
  );
}
