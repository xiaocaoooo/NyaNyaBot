import type {
  APIError,
  AppConfig,
  AuthStatusResponse,
  BotsResponse,
  CommandListener,
  ConfigPatch,
  CronListener,
  ExportSpec,
  EventListener,
  GlobalsResponse,
  LoginPayload,
  PluginConfigResponse,
  PluginDescriptor,
  PluginListItem,
  PluginStateView,
  UpdateGlobalsPayload,
  UpdatePluginConfigPayload,
  UpdatePluginSwitchesPayload,
  UpdatePluginSwitchesResponse,
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

const pluginNameCollator = new Intl.Collator(undefined, {
  numeric: true,
  sensitivity: "base",
});

function ensureArray<T>(value: T[] | null | undefined): T[] {
  return Array.isArray(value) ? value : [];
}

function sortPluginsByName<T extends { name: string; plugin_id: string }>(plugins: T[]): T[] {
  return [...plugins].sort((left, right) => {
    const nameCompare = pluginNameCollator.compare(left.name, right.name);
    if (nameCompare !== 0) {
      return nameCompare;
    }

    return pluginNameCollator.compare(left.plugin_id, right.plugin_id);
  });
}

function normalizePluginState(state: Partial<PluginStateView> | null | undefined): PluginStateView {
  return {
    enabled: state?.enabled ?? true,
    commands: state?.commands ?? {},
    events: state?.events ?? {},
    crons: state?.crons ?? {},
    command_prefix: state?.command_prefix ?? "",
  };
}

function normalizePluginDescriptor(
  plugin: PluginDescriptor & {
    dependencies?: string[] | null;
    exports?: ExportSpec[] | null;
    commands?: CommandListener[] | null;
    events?: EventListener[] | null;
    crons?: CronListener[] | null;
  },
): PluginDescriptor {
  return {
    ...plugin,
    dependencies: ensureArray(plugin.dependencies),
    exports: ensureArray(plugin.exports),
    commands: ensureArray(plugin.commands),
    events: ensureArray(plugin.events),
    crons: ensureArray(plugin.crons),
  };
}

function normalizePluginListItem(
  plugin: PluginListItem & {
    dependencies?: string[] | null;
    exports?: ExportSpec[] | null;
    commands?: CommandListener[] | null;
    events?: EventListener[] | null;
    crons?: CronListener[] | null;
    state?: Partial<PluginStateView> | null;
  },
): PluginListItem {
  return {
    ...normalizePluginDescriptor(plugin),
    state: normalizePluginState(plugin.state),
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
    return requestJSON<PluginListItem[]>("/api/plugins").then((plugins) =>
      sortPluginsByName(plugins.map((plugin) => normalizePluginListItem(plugin))),
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
  updatePluginSwitches(pluginId: string, payload: UpdatePluginSwitchesPayload) {
    return requestJSON<UpdatePluginSwitchesResponse>(
      `/api/plugins/${encodeURIComponent(pluginId)}/switches`,
      {
        method: "PUT",
        body: JSON.stringify(payload),
      },
    ).then((response) => ({
      ...response,
      state: normalizePluginState(response.state),
    }));
  },
  fetchBots() {
    return requestJSON<BotsResponse>("/api/bots");
  },
};
