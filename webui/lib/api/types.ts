export interface AccessControl {
  whitelist_users?: number[];
  blacklist_users?: number[];
  whitelist_groups?: number[];
  blacklist_groups?: number[];
}

export interface Override {
  pattern: string;
  replacement: string;
}

export interface AppConfig {
  onebot: {
    reverse_ws: {
      listen_addr: string;
    };
  };
  webui: {
    listen_addr: string;
    auto_refresh: boolean;
    refresh_interval: number;
  };
  chat_log: {
    database_uri: string;
  };
  trigger_log: {
    enabled: boolean;
    database_uri: string;
    queue_size: number;
    batch_size: number;
    batch_interval: string;
  };
  globals?: Record<string, string>;
  plugins?: Record<string, unknown>;
  message_prefix?: string;
  global_sleep_timeout?: number;
  global_access?: AccessControl;
}

export interface ConfigPatch {
  onebot?: {
    reverse_ws?: {
      listen_addr?: string;
    };
  };
  webui?: {
    listen_addr?: string;
    auto_refresh?: boolean;
    refresh_interval?: number;
  };
  chat_log?: {
    database_uri?: string;
  };
  trigger_log?: {
    enabled?: boolean;
    database_uri?: string;
    queue_size?: number;
    batch_size?: number;
    batch_interval?: string;
  };
  message_prefix?: string;
  global_sleep_timeout?: number;
  global_access?: AccessControl;
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
  status?: "Running" | "Idle" | "Sleeping" | "Crashed";
}

export interface PluginStateView {
  enabled: boolean;
  commands: Record<string, boolean>;
  events: Record<string, boolean>;
  crons: Record<string, boolean>;
  command_prefix?: string;
  enable_sleep?: boolean;
  sleep_timeout?: number;
  status?: "Running" | "Idle" | "Sleeping" | "Crashed" | "Unknown";
  access?: AccessControl;
  command_access?: Record<string, AccessControl>;
  event_access?: Record<string, AccessControl>;
  command_overrides?: Record<string, Override[]>;
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
  enable_sleep?: boolean;
  sleep_timeout?: number;
  status?: "Running" | "Idle" | "Sleeping" | "Crashed" | "Unknown";
  access?: AccessControl;
  command_access?: Record<string, AccessControl>;
  event_access?: Record<string, AccessControl>;
  command_overrides?: Record<string, Override[]>;
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

export interface TriggerLog {
  trace_id: string;
  plugin_id: string;
  listener_id: string;
  listener_type: 'command' | 'event' | 'cron';
  group_id: number;
  user_id: number;
  self_id: number;
  message_id: number;
  message_seq: string;
  trigger_data: Record<string, unknown>;
  success: boolean;
  duration_ms: number;
  error_message: string;
  triggered_at: string; // ISO 8601
  recorded_at: string;  // ISO 8601
}

export interface TriggerLogQuery {
  group_id?: number;
  user_id?: number;
  plugin_id?: string;
  listener_id?: string;
  listener_type?: 'command' | 'event' | 'cron';
  start_time?: string;
  end_time?: string;
  message_seq?: string;
  trace_id?: string;
  success?: boolean;
  sort_by?: 'triggered_at' | 'duration_ms';
  sort_desc?: boolean;
  page?: number;
  page_size?: number;
}

export interface TriggerStatistics {
  total_count: number;
  success_count: number;
  failed_count: number;
  avg_duration_ms: number;
}

export interface TriggerLogsResponse {
  records: TriggerLog[];
  total: number;
  page: number;
  page_size: number;
  stats: TriggerStatistics;
}
