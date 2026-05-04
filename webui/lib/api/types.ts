export interface AppConfig {
  onebot: {
    reverse_ws: {
      listen_addr: string;
    };
  };
  webui: {
    listen_addr: string;
  };
  chat_log: {
    database_uri: string;
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
  chat_log?: {
    database_uri?: string;
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

export interface CronListener {
  name: string;
  id: string;
  description?: string;
  schedule: string;
  handler?: string;
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
  crons: CronListener[];
}

export interface PluginStateView {
  enabled: boolean;
  commands: Record<string, boolean>;
  events: Record<string, boolean>;
  crons: Record<string, boolean>;
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
  crons?: Record<string, boolean>;
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

export interface BotGroup {
  group_id: number;
  group_name: string;
  member_count: number;
  max_member_count?: number;
}

export interface BotInfo {
  self_id: number;
  nickname: string;
  online: boolean;
  remote_addr: string;
  connected_at: string;
  group_count: number;
  groups: BotGroup[];
  recv_count?: number;
  sent_count?: number;
  filtered_self_count?: number;
  filtered_non_group_count?: number;
  dedup_count?: number;
}

export interface BotStats {
  recv_count?: number;
  sent_count?: number;
  filtered_self_count?: number;
  filtered_non_group_count?: number;
  dedup_count?: number;
  start_time?: string;
  uptime?: string;
}

export interface BotsResponse {
  group_chat_only: boolean;
  dedupe_key: string;
  bots: BotInfo[];
  total_bots: number;
  online_bots: number;
  total_groups: number;
  stats?: BotStats;
  // 兼容保留的平铺字段（向后兼容）
  global_recv_count?: number;
  global_sent_count?: number;
  global_start_time?: string;
  global_uptime?: number;
}
