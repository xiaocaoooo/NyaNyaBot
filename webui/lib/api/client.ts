import type {
  APIError,
  AppConfig,
  AuthStatusResponse,
  BotGroup,
  BotInfo,
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
  TriggerLogQuery,
  TriggerLogsResponse,
  TriggerStatistics,
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
    const maybeError = (data as APIError | null)?.error;
    const errorMessage = maybeError ?? `Request failed with status ${response.status}`;
    
    // 输出详细错误信息到控制台
    console.error('=== API Request Error ===');
    console.error('URL:', input);
    console.error('Status:', response.status);
    console.error('Status Text:', response.statusText);
    console.error('Error Message:', errorMessage);
    console.error('Response Data:', data);
    console.error('Timestamp:', new Date().toISOString());
    console.error('========================');
    
    if (response.status === 401 && !shouldSkipUnauthorizedRedirect(input)) {
      redirectToLogin();
    }
    
    throw new Error(errorMessage);
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

function normalizeBotInfo(bot: BotInfo & { groups?: BotGroup[] | null }): BotInfo {
  return {
    ...bot,
    groups: ensureArray(bot.groups),
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
    return requestJSON<BotsResponse>("/api/bots").then((response) => ({
      ...response,
      bots: (response.bots ?? []).map(normalizeBotInfo),
    }));
  },
  queryTriggerLogs(query?: TriggerLogQuery) {
    const params = new URLSearchParams();
    
    if (query) {
      if (query.group_id !== undefined) params.append("group_id", String(query.group_id));
      if (query.user_id !== undefined) params.append("user_id", String(query.user_id));
      if (query.plugin_id) params.append("plugin_id", query.plugin_id);
      if (query.listener_id) params.append("listener_id", query.listener_id);
      if (query.listener_type) params.append("listener_type", query.listener_type);
      if (query.start_time) params.append("start_time", query.start_time);
      if (query.end_time) params.append("end_time", query.end_time);
      if (query.message_seq) params.append("message_seq", query.message_seq);
      if (query.trace_id) params.append("trace_id", query.trace_id);
      if (query.success !== undefined) params.append("success", String(query.success));
      if (query.sort_by) params.append("sort_by", query.sort_by);
      if (query.sort_desc !== undefined) params.append("sort_desc", String(query.sort_desc));
      if (query.page !== undefined) params.append("page", String(query.page));
      if (query.page_size !== undefined) params.append("page_size", String(query.page_size));
    }
    
    const queryString = params.toString();
    const url = queryString ? `/api/trigger-logs?${queryString}` : "/api/trigger-logs";
    
    return requestJSON<TriggerLogsResponse>(url);
  },
  getTriggerLogStats(query?: Omit<TriggerLogQuery, 'page' | 'page_size' | 'sort_by' | 'sort_desc'>) {
    const params = new URLSearchParams();
    
    if (query) {
      if (query.group_id !== undefined) params.append("group_id", String(query.group_id));
      if (query.user_id !== undefined) params.append("user_id", String(query.user_id));
      if (query.plugin_id) params.append("plugin_id", query.plugin_id);
      if (query.listener_id) params.append("listener_id", query.listener_id);
      if (query.listener_type) params.append("listener_type", query.listener_type);
      if (query.start_time) params.append("start_time", query.start_time);
      if (query.end_time) params.append("end_time", query.end_time);
      if (query.message_seq) params.append("message_seq", query.message_seq);
      if (query.trace_id) params.append("trace_id", query.trace_id);
      if (query.success !== undefined) params.append("success", String(query.success));
    }
    
    const queryString = params.toString();
    const url = queryString ? `/api/trigger-logs/stats?${queryString}` : "/api/trigger-logs/stats";
    
    return requestJSON<TriggerStatistics>(url);
  },
};
