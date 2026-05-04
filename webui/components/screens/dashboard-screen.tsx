"use client";

import { Chip, Divider, Spinner } from "@heroui/react";
import { RefreshCw } from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";

import { AppButton } from "@/components/ui/button";
import { useI18n } from "@/components/providers/i18n-provider";
import { AppCard, AppCardBody, AppCardHeader } from "@/components/ui/card";
import { StatusMessage } from "@/components/ui/status-message";
import { apiClient } from "@/lib/api/client";
import type { AppConfig, BotsResponse, PluginDescriptor } from "@/lib/api/types";

function StatItem({ label, value }: { label: string; value: string | number }) {
  return (
    <div className="rounded-lg border border-border/70 bg-surface-elevated/70 p-3">
      <p className="text-xs text-muted">{label}</p>
      <p className="mt-1 text-lg font-semibold text-text">{value}</p>
    </div>
  );
}

export function DashboardScreen() {
  const { t } = useI18n();
  const [config, setConfig] = useState<AppConfig | null>(null);
  const [plugins, setPlugins] = useState<PluginDescriptor[]>([]);
  const [bots, setBots] = useState<BotsResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  const fetchData = useCallback(async () => {
    setLoading(true);
    setError(null);

    try {
      const [configData, pluginData, botsData] = await Promise.all([
        apiClient.fetchConfig(),
        apiClient.fetchPlugins(),
        apiClient.fetchBots(),
      ]);
      setConfig(configData);
      setPlugins(pluginData);
      setBots(botsData);
    } catch (err) {
      setError(err instanceof Error ? err.message : t("dashboard.errorLoad"));
    } finally {
      setLoading(false);
    }
  }, [t]);

  useEffect(() => {
    void fetchData();
  }, [fetchData]);

  const configuredPluginCount = useMemo(() => Object.keys(config?.plugins ?? {}).length, [config]);

  // 优先使用 stats，若不存在则 fallback 到平铺的 global_* 字段
  const globalStats = useMemo(() => {
    if (!bots) return null;
    if (bots.stats) return bots.stats;
    // Fallback 到平铺字段
    return {
      recv_count: bots.global_recv_count,
      sent_count: bots.global_sent_count,
      start_time: bots.global_start_time,
      uptime: bots.global_uptime,
      filtered_self_count: undefined,
      filtered_non_group_count: undefined,
      dedup_count: undefined,
    };
  }, [bots]);

  return (
    <section className="space-y-6">
      <header className="space-y-2">
        <h1 className="text-2xl font-semibold text-text sm:text-3xl">{t("dashboard.title")}</h1>
        <p className="max-w-2xl text-sm text-muted sm:text-base">{t("dashboard.subtitle")}</p>
      </header>

      <div className="flex flex-wrap items-center gap-3">
        <AppButton startContent={<RefreshCw className="h-4 w-4" />} tone="neutral" onPress={fetchData}>
          {t("dashboard.refresh")}
        </AppButton>
        {error ? <StatusMessage tone="error">{error}</StatusMessage> : null}
      </div>

      {loading ? (
        <div className="flex min-h-[260px] items-center justify-center">
          <Spinner color="primary" label={t("dashboard.loading")} labelColor="primary" />
        </div>
      ) : (
        <div className="space-y-4">
          <div className="grid gap-4 lg:grid-cols-12">
            <AppCard className="lg:col-span-5">
              <AppCardHeader>
                <h2 className="text-lg font-semibold text-text">{t("dashboard.runtimeTitle")}</h2>
                <p className="text-sm text-muted">{t("dashboard.runtimeDesc")}</p>
              </AppCardHeader>
              <AppCardBody>
                <div className="grid gap-3 sm:grid-cols-2">
                  <StatItem label={t("dashboard.statLoadedPlugins")} value={plugins.length} />
                  <StatItem label={t("dashboard.statConfiguredPlugins")} value={configuredPluginCount} />
                  <StatItem label={t("dashboard.statWebuiListen")} value={config?.webui.listen_addr ?? "-"} />
                  <StatItem label={t("dashboard.statOnebotListen")} value={config?.onebot.reverse_ws.listen_addr ?? "-"} />
                  {bots && (
                    <>
                      <StatItem label={t("dashboard.statTotalBots")} value={bots.total_bots ?? 0} />
                      <StatItem label={t("dashboard.statOnlineBots")} value={bots.online_bots ?? 0} />
                      <StatItem label={t("dashboard.statTotalGroups")} value={bots.total_groups ?? 0} />
                    </>
                  )}
                </div>
              </AppCardBody>
            </AppCard>

            <AppCard className="lg:col-span-7">
              <AppCardHeader>
                <h2 className="text-lg font-semibold text-text">{t("dashboard.pluginsTitle")}</h2>
                <p className="text-sm text-muted">{t("dashboard.pluginsDesc")}</p>
              </AppCardHeader>
              <AppCardBody>
                {plugins.length === 0 ? (
                  <p className="rounded-lg border border-dashed border-border p-4 text-sm text-muted">{t("dashboard.noPlugins")}</p>
                ) : (
                  <ul className="space-y-2">
                    {plugins.slice(0, 6).map((plugin) => (
                      <li key={plugin.plugin_id} className="rounded-lg border border-border/70 bg-surface-elevated/50 p-3">
                        <div className="flex flex-wrap items-center justify-between gap-2">
                          <div>
                            <p className="font-medium text-text">{plugin.name}</p>
                            <p className="text-xs text-muted">
                              {plugin.plugin_id} · v{plugin.version} · {plugin.author}
                            </p>
                          </div>
                          <div className="flex items-center gap-2">
                            <Chip radius="sm" size="sm" variant="flat">
                              {t("dashboard.commandsCount", { count: plugin.commands.length })}
                            </Chip>
                            <Chip radius="sm" size="sm" variant="flat">
                              {t("dashboard.eventsCount", { count: plugin.events.length })}
                            </Chip>
                          </div>
                        </div>
                      </li>
                    ))}
                  </ul>
                )}
                <Divider className="my-2 bg-border/70" />
                <p className="text-xs text-muted">{t("dashboard.moreHint")}</p>
              </AppCardBody>
            </AppCard>
          </div>

          <AppCard>
            <AppCardHeader>
              <h2 className="text-lg font-semibold text-text">{t("dashboard.botsTitle")}</h2>
              <p className="text-sm text-muted">{t("dashboard.botsDesc")}</p>
            </AppCardHeader>
            <AppCardBody>
              <div className="space-y-3">
                <div className="flex flex-wrap gap-2">
                  <Chip color="primary" radius="sm" size="sm" variant="flat">
                    {t("dashboard.groupChatOnly")}: {bots?.group_chat_only ? t("dashboard.enabled") : t("dashboard.disabled")}
                  </Chip>
                  <Chip radius="sm" size="sm" variant="flat">
                    {t("dashboard.dedupeKey")}: {bots?.dedupe_key ?? "-"}
                  </Chip>
                </div>

                {bots?.bots && bots.bots.length > 0 ? (
                  <ul className="space-y-3">
                    {bots.bots.map((bot) => (
                      <li key={bot.self_id} className="rounded-lg border border-border/70 bg-surface-elevated/50 p-3">
                        <div className="space-y-2">
                          <div className="flex flex-wrap items-center justify-between gap-2">
                            <div className="flex items-center gap-2">
                              <Chip color={bot.online ? "success" : "default"} radius="sm" size="sm" variant="dot">
                                {bot.online ? t("dashboard.online") : t("dashboard.offline")}
                              </Chip>
                              <p className="font-medium text-text">
                                {bot.nickname} ({bot.self_id})
                              </p>
                            </div>
                            <p className="text-xs text-muted">{bot.remote_addr}</p>
                          </div>

                          <div className="flex flex-wrap gap-2 text-xs text-muted">
                            <span>{t("dashboard.connectedAt")}: {new Date(bot.connected_at).toLocaleString()}</span>
                            <span>·</span>
                            <span>{t("dashboard.groupCount")}: {bot.group_count}</span>
                            {bot.recv_count !== undefined && (
                              <>
                                <span>·</span>
                                <span>{t("dashboard.recvCount")}: {bot.recv_count}</span>
                              </>
                            )}
                            {bot.sent_count !== undefined && (
                              <>
                                <span>·</span>
                                <span>{t("dashboard.sentCount")}: {bot.sent_count}</span>
                              </>
                            )}
                          </div>

                          {bot.groups && bot.groups.length > 0 && (
                            <div className="space-y-1">
                              <p className="text-xs font-medium text-muted">{t("dashboard.groups")}:</p>
                              <div className="flex flex-wrap gap-1">
                                {bot.groups.slice(0, 5).map((group) => (
                                  <Chip key={group.group_id} radius="sm" size="sm" variant="bordered">
                                    {group.group_name} ({group.member_count}
                                    {group.max_member_count ? `/${group.max_member_count}` : ""})
                                  </Chip>
                                ))}
                                {bot.groups.length > 5 && (
                                  <Chip radius="sm" size="sm" variant="bordered">
                                    {t("dashboard.moreGroups", { count: bot.groups.length - 5 })}
                                  </Chip>
                                )}
                              </div>
                            </div>
                          )}

                          {(bot.filtered_self_count !== undefined ||
                            bot.filtered_non_group_count !== undefined ||
                            bot.dedup_count !== undefined) && (
                            <div className="flex flex-wrap gap-2 text-xs text-muted">
                              {bot.filtered_self_count !== undefined && (
                                <span>{t("dashboard.filteredSelf")}: {bot.filtered_self_count}</span>
                              )}
                              {bot.filtered_non_group_count !== undefined && (
                                <>
                                  <span>·</span>
                                  <span>{t("dashboard.filteredNonGroup")}: {bot.filtered_non_group_count}</span>
                                </>
                              )}
                              {bot.dedup_count !== undefined && (
                                <>
                                  <span>·</span>
                                  <span>{t("dashboard.dedupCount")}: {bot.dedup_count}</span>
                                </>
                              )}
                            </div>
                          )}
                        </div>
                      </li>
                    ))}
                  </ul>
                ) : (
                  <p className="rounded-lg border border-dashed border-border p-4 text-sm text-muted">{t("dashboard.noBots")}</p>
                )}

                {globalStats && (
                  <>
                    <Divider className="my-2 bg-border/70" />
                    <div className="flex flex-wrap gap-2 text-xs text-muted">
                      <span className="font-medium">{t("dashboard.globalStats")}:</span>
                      {globalStats.recv_count !== undefined && <span>{t("dashboard.recvCount")}: {globalStats.recv_count}</span>}
                      {globalStats.sent_count !== undefined && (
                        <>
                          <span>·</span>
                          <span>{t("dashboard.sentCount")}: {globalStats.sent_count}</span>
                        </>
                      )}
                      {globalStats.filtered_self_count !== undefined && (
                        <>
                          <span>·</span>
                          <span>{t("dashboard.filteredSelf")}: {globalStats.filtered_self_count}</span>
                        </>
                      )}
                      {globalStats.filtered_non_group_count !== undefined && (
                        <>
                          <span>·</span>
                          <span>{t("dashboard.filteredNonGroup")}: {globalStats.filtered_non_group_count}</span>
                        </>
                      )}
                      {globalStats.dedup_count !== undefined && (
                        <>
                          <span>·</span>
                          <span>{t("dashboard.dedupCount")}: {globalStats.dedup_count}</span>
                        </>
                      )}
                      {globalStats.start_time !== undefined && (
                        <>
                          <span>·</span>
                          <span>{t("dashboard.startTime")}: {new Date(globalStats.start_time).toLocaleString()}</span>
                        </>
                      )}
                      {globalStats.uptime !== undefined && (
                        <>
                          <span>·</span>
                          <span>{t("dashboard.uptime")}: {globalStats.uptime}</span>
                        </>
                      )}
                    </div>
                  </>
                )}
              </div>
            </AppCardBody>
          </AppCard>
        </div>
      )}
    </section>
  );
}
