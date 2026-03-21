"use client";

import { Spinner } from "@heroui/react";
import { FormEvent, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";

import { useI18n } from "@/components/providers/i18n-provider";
import { AppButton } from "@/components/ui/button";
import { AppCard, AppCardBody, AppCardFooter, AppCardHeader } from "@/components/ui/card";
import { FormField } from "@/components/ui/form-field";
import { AppInput } from "@/components/ui/input";
import { StatusMessage } from "@/components/ui/status-message";
import { apiClient } from "@/lib/api/client";

function sanitizeNextPath(value: string | null): string {
  if (!value) {
    return "/";
  }
  const next = value.trim();
  if (!next.startsWith("/") || next.startsWith("//")) {
    return "/";
  }
  return next;
}

function buildLoginURL(nextPath: string): string {
  if (nextPath === "/") {
    return "/login/";
  }
  return `/login/?next=${encodeURIComponent(nextPath)}`;
}

export function LoginScreen() {
  const { t } = useI18n();
  const router = useRouter();
  const searchParams = useSearchParams();
  const autoTried = useRef(false);

  const nextPath = useMemo(() => sanitizeNextPath(searchParams.get("next")), [searchParams]);

  const [password, setPassword] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [autoSigningIn, setAutoSigningIn] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const mapLoginErrorMessage = useCallback(
    (err: unknown): string => {
      if (!(err instanceof Error)) {
        return t("login.errorGeneric");
      }
      const normalized = err.message.trim().toLowerCase();
      if (normalized === "invalid password") {
        return t("login.errorInvalid");
      }
      if (normalized === "unauthorized") {
        return t("login.errorInvalid");
      }
      return err.message || t("login.errorGeneric");
    },
    [t],
  );

  const login = async (rawPassword: string) => {
    setSubmitting(true);
    setError(null);
    try {
      await apiClient.login({ password: rawPassword });
      router.replace(nextPath);
    } catch (err) {
      setError(mapLoginErrorMessage(err));
    } finally {
      setSubmitting(false);
    }
  };

  const onSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    await login(password.trim());
  };

  useEffect(() => {
    const passwordFromURL = searchParams.get("password");
    if (!passwordFromURL || autoTried.current) {
      return;
    }
    autoTried.current = true;

    setPassword(passwordFromURL);
    setAutoSigningIn(true);

    if (typeof window !== "undefined") {
      window.history.replaceState(null, "", buildLoginURL(nextPath));
    }

    void (async () => {
      try {
        await apiClient.login({ password: passwordFromURL });
        router.replace(nextPath);
      } catch (err) {
        setError(mapLoginErrorMessage(err));
      } finally {
        setAutoSigningIn(false);
      }
    })();
  }, [mapLoginErrorMessage, nextPath, router, searchParams]);

  return (
    <section className="mx-auto flex min-h-[70vh] w-full max-w-md items-center">
      <AppCard className="w-full">
        <AppCardHeader>
          <h1 className="text-2xl font-semibold text-text">{t("login.title")}</h1>
          <p className="text-sm text-muted">{t("login.subtitle")}</p>
        </AppCardHeader>

        <AppCardBody>
          <form className="space-y-4" onSubmit={onSubmit}>
            <FormField label={t("login.passwordLabel")} required>
              <AppInput
                aria-label={t("login.passwordAria")}
                placeholder={t("login.passwordPlaceholder")}
                type="password"
                value={password}
                onValueChange={setPassword}
              />
            </FormField>

            {autoSigningIn ? (
              <div className="flex items-center gap-2 text-sm text-muted">
                <Spinner color="primary" size="sm" />
                <span>{t("login.autoSigningIn")}</span>
              </div>
            ) : null}

            {error ? <StatusMessage tone="error">{error}</StatusMessage> : null}

            <AppButton className="w-full" color="primary" isLoading={submitting} type="submit">
              {submitting ? t("login.submitting") : t("login.submit")}
            </AppButton>
          </form>
        </AppCardBody>

        <AppCardFooter>
          <p className="text-xs text-muted">{t("login.hint")}</p>
        </AppCardFooter>
      </AppCard>
    </section>
  );
}
