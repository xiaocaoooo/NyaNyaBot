"use client";

import Link from "next/link";

import { useI18n } from "@/components/providers/i18n-provider";
import { AppButton } from "@/components/ui/button";

export default function NotFound() {
  const { t } = useI18n();

  return (
    <section className="mx-auto flex min-h-[50vh] max-w-xl flex-col items-center justify-center gap-4 text-center">
      <p className="rounded-full border border-border bg-surface-elevated px-3 py-1 text-xs uppercase tracking-wide text-muted">404</p>
      <h1 className="text-2xl font-semibold text-text">{t("notFound.title")}</h1>
      <p className="text-sm text-muted">{t("notFound.description")}</p>
      <AppButton as={Link} href="/" tone="primary">
        {t("notFound.backHome")}
      </AppButton>
    </section>
  );
}
