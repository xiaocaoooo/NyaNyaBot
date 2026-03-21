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

export interface PluginDescriptor {
  name: string;
  plugin_id: string;
  version: string;
  author: string;
  description: string;
  config?: PluginConfigSpec;
  commands: CommandListener[];
  events: EventListener[];
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

export interface APIError {
  error?: string;
}

export interface AuthStatusResponse {
  authenticated: boolean;
}

export interface LoginPayload {
  password: string;
}
