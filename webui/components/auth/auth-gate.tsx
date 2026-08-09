"use client";

import { Spinner } from "@heroui/react";
import { usePathname } from "next/navigation";
import { useEffect, useState, type ReactNode } from "react";

import { apiClient } from "@/lib/api/client";

function isLoginPath(pathname: string | null): boolean {
  if (!pathname) {
    return false;
  }
  return pathname === "/login" || pathname.startsWith("/login/");
}

function redirectToLogin(pathname: string, search: string) {
  const next = `${pathname}${search}`;
  const target = `/login/?next=${encodeURIComponent(next || "/")}`;
  window.location.assign(target);
}

interface AuthGateProps {
  children: ReactNode;
}

export function AuthGate({ children }: AuthGateProps) {
  const pathname = usePathname();
  const onLogin = isLoginPath(pathname);
  const [ready, setReady] = useState(onLogin);

  useEffect(() => {
    if (onLogin) {
      setReady(true);
      return;
    }

    let cancelled = false;
    setReady(false);

    void (async () => {
      try {
        const status = await apiClient.fetchAuthStatus();
        if (cancelled) {
          return;
        }
        if (!status.authenticated) {
          redirectToLogin(window.location.pathname, window.location.search);
          return;
        }
        setReady(true);
      } catch {
        // Network/backend blip: let page content try its own API calls / 401 redirect.
        if (!cancelled) {
          setReady(true);
        }
      }
    })();

    return () => {
      cancelled = true;
    };
  }, [onLogin, pathname]);

  if (!ready) {
    return (
      <div className="flex min-h-[50vh] items-center justify-center">
        <Spinner color="primary" label="Loading" labelColor="primary" />
      </div>
    );
  }

  return children;
}
