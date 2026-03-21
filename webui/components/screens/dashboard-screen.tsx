"use client";

import { Chip, Divider, Spinner } from "@heroui/react";
import { RefreshCw } from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";

import { AppButton } from "@/components/ui/button";
import { useI18n } from "@/components/providers/i18n-provider";
import { AppCard, AppCardBody, AppCardHeader } from "@/components/ui/card";
import { StatusMessage } from "@/components/ui/status-message";
import { apiClient } from "@/lib/api/client";
import type { AppConfig, PluginDescriptor } from "@/lib/api/types";

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
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  const fetchData = useCallback(async () => {
    setLoading(true);
    setError(null);

    try {
      const [configData, pluginData] = await Promise.all([apiClient.fetchConfig(), apiClient.fetchPlugins()]);
      setConfig(configData);
      setPlugins(pluginData);
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
      )}
    </section>
  );
}
