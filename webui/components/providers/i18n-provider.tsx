"use client";

import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";

import { defaultLocale, isLocale, localeStorageKey, messages, type Locale, type MessageKey } from "@/lib/i18n/messages";

export interface TranslateValues {
  [key: string]: number | string;
}

export type TranslateFn = (key: MessageKey, values?: TranslateValues) => string;

interface I18nContextValue {
  locale: Locale;
  setLocale: (nextLocale: Locale) => void;
  t: TranslateFn;
}

const I18nContext = createContext<I18nContextValue | null>(null);

function formatMessage(template: string, values?: TranslateValues): string {
  if (!values) {
    return template;
  }

  return template.replace(/\{\{(\w+)\}\}/g, (_, key: string) => {
    const value = values[key];
    return value === undefined ? "" : String(value);
  });
}

function detectPreferredLocale(): Locale {
  if (typeof window === "undefined") {
    return defaultLocale;
  }

  const saved = window.localStorage.getItem(localeStorageKey);
  if (saved && isLocale(saved)) {
    return saved;
  }

  const candidates = (window.navigator.languages && window.navigator.languages.length > 0
    ? window.navigator.languages
    : [window.navigator.language]
  ).map((item) => item.toLowerCase());

  for (const lang of candidates) {
    if (lang.startsWith("zh-tw") || lang.startsWith("zh-hk") || lang.startsWith("zh-mo") || lang.includes("hant")) {
      return "zh-TW";
    }
    if (lang.startsWith("zh")) {
      return "zh-CN";
    }
    if (lang.startsWith("ja")) {
      return "ja-JP";
    }
    if (lang.startsWith("en")) {
      return "en-US";
    }
  }

  return defaultLocale;
}

export function I18nProvider({ children }: { children: ReactNode }) {
  const [locale, setLocaleState] = useState<Locale>(defaultLocale);

  useEffect(() => {
    setLocaleState(detectPreferredLocale());
  }, []);

  useEffect(() => {
    if (typeof window === "undefined") {
      return;
    }

    window.localStorage.setItem(localeStorageKey, locale);
    document.documentElement.lang = locale;
  }, [locale]);

  const setLocale = useCallback((nextLocale: Locale) => {
    setLocaleState(nextLocale);
  }, []);

  const t = useCallback<TranslateFn>(
    (key, values) => {
      const localized = messages[locale][key] ?? messages[defaultLocale][key] ?? key;
      return formatMessage(localized, values);
    },
    [locale],
  );

  const contextValue = useMemo(
    () => ({
      locale,
      setLocale,
      t,
    }),
    [locale, setLocale, t],
  );

  return <I18nContext.Provider value={contextValue}>{children}</I18nContext.Provider>;
}

export function useI18n(): I18nContextValue {
  const context = useContext(I18nContext);
  if (!context) {
    throw new Error("useI18n must be used within I18nProvider");
  }
  return context;
}
