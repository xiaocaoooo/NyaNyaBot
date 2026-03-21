"use client";

import { useI18n } from "@/components/providers/i18n-provider";
import { supportedLocales, type Locale } from "@/lib/i18n/messages";
import { cn } from "@/lib/utils/cn";

const localeLabelMap: Record<Locale, "locale.zhCN" | "locale.zhTW" | "locale.enUS" | "locale.jaJP"> = {
  "zh-CN": "locale.zhCN",
  "zh-TW": "locale.zhTW",
  "en-US": "locale.enUS",
  "ja-JP": "locale.jaJP",
};

export function LocaleSwitcher() {
  const { locale, setLocale, t } = useI18n();

  return (
    <div aria-label={t("locale.switchAria")} className="flex items-center gap-1 rounded-lg border border-border/70 bg-surface-elevated/80 p-1" role="group">
      {supportedLocales.map((item) => {
        const active = item === locale;

        return (
          <button
            key={item}
            className={cn(
              "rounded-md px-2 py-1 text-xs font-semibold transition",
              "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary focus-visible:ring-offset-2 focus-visible:ring-offset-bg",
              active ? "bg-primary text-primary-foreground" : "text-muted hover:bg-surface hover:text-text",
            )}
            type="button"
            onClick={() => setLocale(item)}
          >
            {t(localeLabelMap[item])}
          </button>
        );
      })}
    </div>
  );
}
