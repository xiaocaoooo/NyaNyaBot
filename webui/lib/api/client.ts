import type {
  APIError,
  AppConfig,
  AuthStatusResponse,
  CommandListener,
  ConfigPatch,
  ExportSpec,
  EventListener,
  GlobalsResponse,
  LoginPayload,
  PluginConfigResponse,
  PluginDescriptor,
  UpdateGlobalsPayload,
  UpdatePluginConfigPayload,
} from "@/lib/api/types";

function redirectToLogin() {
  if (typeof window === "undefined") {
    return;
  }
  if (window.location.pathname === "/login" || window.location.pathname.startsWith("/login/")) {
    return;
  }

  const next = `${window.location.pathname}${window.location.search}`;
  const target = `/login/?next=${encodeURIComponent(next || "/")}`;
  window.location.assign(target);
}

function shouldSkipUnauthorizedRedirect(input: string): boolean {
  return input === "/api/auth/login" || input === "/api/auth/logout" || input === "/api/auth/status";
}

async function requestJSON<T>(input: string, init?: RequestInit): Promise<T> {
  const response = await fetch(input, {
    ...init,
    headers: {
      "Content-Type": "application/json",
      ...(init?.headers ?? {}),
    },
    cache: "no-store",
    credentials: "same-origin",
  });

  const text = await response.text();
  let data: T | APIError | null = null;
  if (text) {
    try {
      data = JSON.parse(text) as T | APIError;
    } catch {
      data = null;
    }
  }

  if (!response.ok) {
    if (response.status === 401 && !shouldSkipUnauthorizedRedirect(input)) {
      redirectToLogin();
    }
    const maybeError = (data as APIError | null)?.error;
    throw new Error(maybeError ?? `Request failed with status ${response.status}`);
  }

  return data as T;
}

function ensureArray<T>(value: T[] | null | undefined): T[] {
  return Array.isArray(value) ? value : [];
}

function normalizePluginDescriptor(
  plugin: PluginDescriptor & {
    dependencies?: string[] | null;
    exports?: ExportSpec[] | null;
    commands?: CommandListener[] | null;
    events?: EventListener[] | null;
  },
): PluginDescriptor {
  return {
    ...plugin,
    dependencies: ensureArray(plugin.dependencies),
    exports: ensureArray(plugin.exports),
    commands: ensureArray(plugin.commands),
    events: ensureArray(plugin.events),
  };
}

export const apiClient = {
  login(payload: LoginPayload) {
    return requestJSON<{ ok: boolean }>("/api/auth/login", {
      method: "POST",
      body: JSON.stringify(payload),
    });
  },
  logout() {
    return requestJSON<{ ok: boolean }>("/api/auth/logout", {
      method: "POST",
    });
  },
  fetchAuthStatus() {
    return requestJSON<AuthStatusResponse>("/api/auth/status");
  },
  fetchConfig() {
    return requestJSON<AppConfig>("/api/config");
  },
  updateConfig(payload: ConfigPatch) {
    return requestJSON<AppConfig>("/api/config", {
      method: "PUT",
      body: JSON.stringify(payload),
    });
  },
  fetchGlobals() {
    return requestJSON<GlobalsResponse>("/api/globals");
  },
  updateGlobals(payload: UpdateGlobalsPayload) {
    return requestJSON<GlobalsResponse>("/api/globals", {
      method: "PUT",
      body: JSON.stringify(payload),
    });
  },
  fetchPlugins() {
    return requestJSON<PluginDescriptor[]>("/api/plugins").then((plugins) =>
      plugins.map((plugin) => normalizePluginDescriptor(plugin)),
    );
  },
  fetchPluginConfig(pluginId: string) {
    return requestJSON<PluginConfigResponse>(`/api/plugins/${encodeURIComponent(pluginId)}/config`);
  },
  updatePluginConfig(pluginId: string, payload: UpdatePluginConfigPayload) {
    return requestJSON<{ ok: boolean }>(`/api/plugins/${encodeURIComponent(pluginId)}/config`, {
      method: "PUT",
      body: JSON.stringify(payload),
    });
  },
};
