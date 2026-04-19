export interface AppConfig {
  onebot: {
    reverse_ws: {
      listen_addr: string;
    };
  };
  webui: {
    listen_addr: string;
  };
  globals?: Record<string, string>;
  plugins?: Record<string, unknown>;
  message_prefix?: string;
}

export interface ConfigPatch {
  onebot?: {
    reverse_ws?: {
      listen_addr?: string;
    };
  };
  webui?: {
    listen_addr?: string;
  };
  message_prefix?: string;
}

export interface PluginConfigSpec {
  version?: string;
  description?: string;
  schema?: unknown;
  default?: unknown;
}

export interface CommandListener {
  id: string;
  name: string;
  description: string;
  pattern: string;
  match_raw: boolean;
  handler: string;
}

export interface EventListener {
  id: string;
  name: string;
  description: string;
  event: string;
  handler: string;
}

export interface ExportSpec {
  name: string;
  description: string;
  params_schema: unknown;
  result_schema: unknown;
}

export interface PluginDescriptor {
  name: string;
  plugin_id: string;
  version: string;
  author: string;
  description: string;
  dependencies: string[];
  exports: ExportSpec[];
  config?: PluginConfigSpec;
  commands: CommandListener[];
  events: EventListener[];
}

export interface PluginStateView {
  enabled: boolean;
  commands: Record<string, boolean>;
  events: Record<string, boolean>;
  command_prefix?: string;
}

export interface PluginListItem extends PluginDescriptor {
  state: PluginStateView;
}

export interface GlobalsResponse {
  globals: Record<string, string>;
}

export interface PluginConfigResponse {
  plugin_id: string;
  config: Record<string, unknown>;
}

export interface UpdateGlobalsPayload {
  globals: Record<string, string>;
}

export interface UpdatePluginConfigPayload {
  config: Record<string, unknown>;
}

export interface UpdatePluginSwitchesPayload {
  enabled?: boolean;
  commands?: Record<string, boolean>;
  events?: Record<string, boolean>;
  prefix?: string;
}

export interface UpdatePluginSwitchesResponse {
  ok: boolean;
  state: PluginStateView;
}

export interface APIError {
  error?: string;
}

export interface AuthStatusResponse {
  authenticated: boolean;
}

export interface LoginPayload {
  password: string;
}
