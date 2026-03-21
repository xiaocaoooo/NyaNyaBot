"use client";

import { HeroUIProvider } from "@heroui/react";
import { useRouter } from "next/navigation";
import { ThemeProvider, useTheme } from "next-themes";
import type { ReactNode } from "react";
import { useEffect } from "react";

import { I18nProvider } from "@/components/providers/i18n-provider";

interface AppProvidersProps {
  children: ReactNode;
}

function FirstVisitThemeInitializer() {
  const { theme, resolvedTheme, setTheme } = useTheme();

  useEffect(() => {
    if (theme !== "system" || !resolvedTheme) {
      return;
    }

    setTheme(resolvedTheme);
  }, [resolvedTheme, setTheme, theme]);

  return null;
}

export function AppProviders({ children }: AppProvidersProps) {
  const router = useRouter();

  return (
    <HeroUIProvider navigate={router.push}>
      <ThemeProvider
        attribute="data-theme"
        defaultTheme="system"
        disableTransitionOnChange
        enableSystem
        themes={["light", "dark"]}
      >
        <FirstVisitThemeInitializer />
        <I18nProvider>{children}</I18nProvider>
      </ThemeProvider>
    </HeroUIProvider>
  );
}
