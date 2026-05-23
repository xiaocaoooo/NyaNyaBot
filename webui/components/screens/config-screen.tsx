"use client";

import { Divider, Spinner, Switch } from "@heroui/react";
import { Plus, RefreshCw, Save, Trash2, Shield } from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";

import { LocaleSwitcher } from "@/components/i18n/locale-switcher";
import { AppButton } from "@/components/ui/button";
import { AppCard, AppCardBody, AppCardFooter, AppCardHeader } from "@/components/ui/card";
import { FormField } from "@/components/ui/form-field";
import { AppInput } from "@/components/ui/input";
import { useI18n } from "@/components/providers/i18n-provider";
import { StatusMessage } from "@/components/ui/status-message";
import { AccessControlPanel } from "@/components/ui/access-control-panel";
import { apiClient } from "@/lib/api/client";
import { AccessControl } from "@/lib/api/types";

interface GlobalRow {
  id: string;
  key: string;
  value: string;
}

function createRow(seed = ""): GlobalRow {
  return {
    id: Math.random().toString(36).slice(2),
    key: seed,
    value: "",
  };
}

export function ConfigScreen() {
  const { t } = useI18n();
  const [webuiAddr, setWebuiAddr] = useState("");
  const [autoRefresh, setAutoRefresh] = useState(true);
  const [refreshInterval, setRefreshInterval] = useState("1");
  const [reverseWSAddr, setReverseWSAddr] = useState("");
  const [chatLogDatabaseURI, setChatLogDatabaseURI] = useState("");
  const [messagePrefix, setMessagePrefix] = useState("");
  const [globalSleepTimeout, setGlobalSleepTimeout] = useState("60");
  const [globalsRows, setGlobalsRows] = useState<GlobalRow[]>([createRow()]);

  // Trigger log config states
  const [triggerLogEnabled, setTriggerLogEnabled] = useState(false);
  const [triggerLogDatabaseURI, setTriggerLogDatabaseURI] = useState("");
  const [triggerLogQueueSize, setTriggerLogQueueSize] = useState("1000");
  const [triggerLogBatchSize, setTriggerLogBatchSize] = useState("100");
  const [triggerLogBatchInterval, setTriggerLogBatchInterval] = useState("5s");

  // Access control states
  const [whitelistUsers, setWhitelistUsers] = useState("");
  const [blacklistUsers, setBlacklistUsers] = useState("");
  const [whitelistGroups, setWhitelistGroups] = useState("");
  const [blacklistGroups, setBlacklistGroups] = useState("");

  const [loading, setLoading] = useState(true);
  const [savingConfig, setSavingConfig] = useState(false);
  const [savingGlobals, setSavingGlobals] = useState(false);
  const [savingPrefix, setSavingPrefix] = useState(false);
  const [savingTriggerLog, setSavingTriggerLog] = useState(false);
  const [savingAccessControl, setSavingAccessControl] = useState(false);

  const [error, setError] = useState<string | null>(null);
  const [status, setStatus] = useState<string | null>(null);

  const loadData = useCallback(async () => {
    setLoading(true);
    setError(null);

    try {
      const [configRes, globalsRes] = await Promise.all([apiClient.fetchConfig(), apiClient.fetchGlobals()]);
      setWebuiAddr(configRes.webui.listen_addr ?? "");
      setAutoRefresh(configRes.webui.auto_refresh ?? true);
      setRefreshInterval(String(configRes.webui.refresh_interval ?? 1));
      setReverseWSAddr(configRes.onebot.reverse_ws.listen_addr ?? "");
      setChatLogDatabaseURI(configRes.chat_log?.database_uri ?? "");
      setMessagePrefix(configRes.message_prefix ?? "");
      setGlobalSleepTimeout(String(configRes.global_sleep_timeout ?? 60));

      // Load trigger log config
      setTriggerLogEnabled(configRes.trigger_log?.enabled ?? false);
      setTriggerLogDatabaseURI(configRes.trigger_log?.database_uri ?? "");
      setTriggerLogQueueSize(String(configRes.trigger_log?.queue_size ?? 1000));
      setTriggerLogBatchSize(String(configRes.trigger_log?.batch_size ?? 100));
      setTriggerLogBatchInterval(configRes.trigger_log?.batch_interval ?? "5s");

      // Load access control
      setWhitelistUsers(configRes.global_access?.whitelist_users?.join("\n") ?? "");
      setBlacklistUsers(configRes.global_access?.blacklist_users?.join("\n") ?? "");
      setWhitelistGroups(configRes.global_access?.whitelist_groups?.join("\n") ?? "");
      setBlacklistGroups(configRes.global_access?.blacklist_groups?.join("\n") ?? "");

      const rows = Object.entries(globalsRes.globals ?? {}).map(([key, value]) => ({
        id: Math.random().toString(36).slice(2),
        key,
        value,
      }));

      setGlobalsRows(rows.length > 0 ? rows : [createRow()]);
    } catch (err) {
      setError(err instanceof Error ? err.message : t("config.errorLoad"));
    } finally {
      setLoading(false);
    }
  }, [t]);

  useEffect(() => {
    void loadData();
  }, [loadData]);

  const globalsPayload = useMemo(() => {
    const map: Record<string, string> = {};

    for (const row of globalsRows) {
      const key = row.key.trim();
      if (!key) {
        continue;
      }
      map[key] = row.value.trim();
    }

    return map;
  }, [globalsRows]);

  const updateGlobalRow = (id: string, patch: Partial<GlobalRow>) => {
    setGlobalsRows((current) => current.map((row) => (row.id === id ? { ...row, ...patch } : row)));
  };

  const removeGlobalRow = (id: string) => {
    setGlobalsRows((current) => {
      const next = current.filter((row) => row.id !== id);
      return next.length === 0 ? [createRow()] : next;
    });
  };

  const saveConfig = async () => {
    setSavingConfig(true);
    setStatus(null);
    setError(null);

    try {
      await apiClient.updateConfig({
        onebot: {
          reverse_ws: {
            listen_addr: reverseWSAddr.trim(),
          },
        },
        webui: {
          listen_addr: webuiAddr.trim(),
          auto_refresh: autoRefresh,
          refresh_interval: Number.parseInt(refreshInterval, 10) || 1,
        },
        chat_log: {
          database_uri: chatLogDatabaseURI.trim(),
        },
        global_sleep_timeout: Number.parseInt(globalSleepTimeout, 10) || 60,
      });
      setStatus(t("config.statusSaveBasic"));
    } catch (err) {
      setError(err instanceof Error ? err.message : t("config.errorSaveBasic"));
    } finally {
      setSavingConfig(false);
    }
  };

  const saveGlobals = async () => {
    setSavingGlobals(true);
    setStatus(null);
    setError(null);

    try {
      await apiClient.updateGlobals({ globals: globalsPayload });
      setStatus(t("config.statusSaveGlobals"));
      await loadData();
    } catch (err) {
      setError(err instanceof Error ? err.message : t("config.errorSaveGlobals"));
    } finally {
      setSavingGlobals(false);
    }
  };

  const savePrefix = async () => {
    setSavingPrefix(true);
    setStatus(null);
    setError(null);

    try {
      await apiClient.updateConfig({
        message_prefix: messagePrefix.trim() || undefined,
      });
      setStatus(t("config.statusSavePrefix"));
    } catch (err) {
      setError(err instanceof Error ? err.message : t("config.errorSavePrefix"));
    } finally {
      setSavingPrefix(false);
    }
  };

  const saveTriggerLog = async () => {
    setSavingTriggerLog(true);
    setStatus(null);
    setError(null);

    try {
      const queueSize = parseInt(triggerLogQueueSize, 10);
      const batchSize = parseInt(triggerLogBatchSize, 10);

      if (isNaN(queueSize) || queueSize <= 0) {
        setError(t("config.errorTriggerLogQueueSize"));
        return;
      }

      if (isNaN(batchSize) || batchSize <= 0) {
        setError(t("config.errorTriggerLogBatchSize"));
        return;
      }

      await apiClient.updateConfig({
        trigger_log: {
          enabled: triggerLogEnabled,
          database_uri: triggerLogDatabaseURI.trim(),
          queue_size: queueSize,
          batch_size: batchSize,
          batch_interval: triggerLogBatchInterval.trim(),
        },
      });
      setStatus(t("config.statusSaveTriggerLog"));
    } catch (err) {
      setError(err instanceof Error ? err.message : t("config.errorSaveTriggerLog"));
    } finally {
      setSavingTriggerLog(false);
    }
  };

  const parseIds = (raw: string): number[] => {
    return raw
      .split(/[\n,]/)
      .map((s) => s.trim())
      .filter((s) => s !== "")
      .map((s) => parseInt(s, 10))
      .filter((n) => !isNaN(n));
  };

  const saveAccessControl = async () => {
    setSavingAccessControl(true);
    setStatus(null);
    setError(null);

    try {
      const global_access: AccessControl = {
        whitelist_users: parseIds(whitelistUsers),
        blacklist_users: parseIds(blacklistUsers),
        whitelist_groups: parseIds(whitelistGroups),
        blacklist_groups: parseIds(blacklistGroups),
      };

      await apiClient.updateConfig({
        global_access,
      });
      setStatus(t("config.statusSaveAccessControl"));
    } catch (err) {
      setError(err instanceof Error ? err.message : t("config.errorSaveAccessControl"));
    } finally {
      setSavingAccessControl(false);
    }
  };

  if (loading) {
    return (
      <div className="flex min-h-[260px] items-center justify-center">
        <Spinner color="primary" label={t("config.loading")} labelColor="primary" />
      </div>
    );
  }

  return (
    <section className="space-y-6">
      <header className="space-y-2">
        <h1 className="text-2xl font-semibold text-text sm:text-3xl">{t("config.title")}</h1>
        <p className="max-w-2xl text-sm text-muted sm:text-base">{t("config.subtitle")}</p>
      </header>

      <div className="flex flex-wrap items-center gap-3">
        <AppButton startContent={<RefreshCw className="h-4 w-4" />} tone="neutral" onPress={loadData}>
          {t("config.refresh")}
        </AppButton>
        {status ? <StatusMessage tone="success">{status}</StatusMessage> : null}
        {error ? <StatusMessage tone="error">{error}</StatusMessage> : null}
      </div>

      <div className="grid gap-4 lg:grid-cols-12">
        <AppCard className="lg:col-span-12">
          <AppCardHeader>
            <h2 className="text-lg font-semibold text-text">{t("config.languageTitle")}</h2>
            <p className="text-sm text-muted">{t("config.languageDesc")}</p>
          </AppCardHeader>
          <AppCardBody>
            <LocaleSwitcher />
          </AppCardBody>
        </AppCard>

        <AppCard className="lg:col-span-5">
          <AppCardHeader>
            <h2 className="text-lg font-semibold text-text">{t("config.listenTitle")}</h2>
          </AppCardHeader>
          <AppCardBody>
            <FormField
              description={t("config.webuiDesc")}
              label={t("config.webuiLabel")}
              required
            >
              <AppInput
                aria-label={t("config.webuiAria")}
                placeholder="127.0.0.1:3000"
                value={webuiAddr}
                onValueChange={setWebuiAddr}
              />
            </FormField>

            <FormField
              description={t("config.webuiAutoRefreshDesc")}
              label={t("config.webuiAutoRefreshLabel")}
            >
              <Switch
                isSelected={autoRefresh}
                onValueChange={setAutoRefresh}
                aria-label={t("config.webuiAutoRefreshLabel")}
              >
                {autoRefresh ? t("config.enabled") : t("config.disabled")}
              </Switch>
            </FormField>

            {autoRefresh && (
              <FormField
                description={t("config.webuiRefreshIntervalDesc")}
                label={t("config.webuiRefreshIntervalLabel")}
              >
                <AppInput
                  aria-label={t("config.webuiRefreshIntervalLabel")}
                  placeholder="1"
                  type="number"
                  value={refreshInterval}
                  onValueChange={setRefreshInterval}
                />
              </FormField>
            )}

            <FormField
              description={t("config.onebotDesc")}
              label={t("config.onebotLabel")}
              required
            >
              <AppInput
                aria-label={t("config.onebotAria")}
                placeholder="0.0.0.0:3001"
                value={reverseWSAddr}
                onValueChange={setReverseWSAddr}
              />
            </FormField>

            <FormField
              description={t("config.chatLogDatabaseDesc")}
              label={t("config.chatLogDatabaseLabel")}
            >
              <AppInput
                aria-label={t("config.chatLogDatabaseAria")}
                placeholder="postgres://user:pass@localhost:5432/nyanyabot?sslmode=disable"
                value={chatLogDatabaseURI}
                onValueChange={setChatLogDatabaseURI}
              />
            </FormField>

            <FormField
              description={t("config.globalSleepDesc")}
              label={t("config.globalSleepLabel")}
            >
              <AppInput
                aria-label={t("config.globalSleepAria")}
                placeholder="60"
                type="number"
                value={globalSleepTimeout}
                onValueChange={setGlobalSleepTimeout}
              />
            </FormField>
          </AppCardBody>
          <AppCardFooter>
            <AppButton
              color="primary"
              isLoading={savingConfig}
              startContent={<Save className="h-4 w-4" />}
              onPress={saveConfig}
            >
              {t("config.saveBasic")}
            </AppButton>
          </AppCardFooter>
        </AppCard>

        <AppCard className="lg:col-span-12">
          <AppCardHeader>
            <div className="flex items-center gap-2">
              <Shield className="h-5 w-5 text-primary" />
              <h2 className="text-lg font-semibold text-text">{t("config.accessControlTitle")}</h2>
            </div>
            <p className="text-sm text-muted">{t("config.accessControlDesc")}</p>
          </AppCardHeader>
          <AppCardBody>
            <AccessControlPanel
              whitelistUsers={whitelistUsers}
              setWhitelistUsers={setWhitelistUsers}
              blacklistUsers={blacklistUsers}
              setBlacklistUsers={setBlacklistUsers}
              whitelistGroups={whitelistGroups}
              setWhitelistGroups={setWhitelistGroups}
              blacklistGroups={blacklistGroups}
              setBlacklistGroups={setBlacklistGroups}
            />
          </AppCardBody>
          <AppCardFooter>
            <AppButton
              color="primary"
              isLoading={savingAccessControl}
              startContent={<Save className="h-4 w-4" />}
              onPress={saveAccessControl}
            >
              {t("config.saveAccessControl")}
            </AppButton>
          </AppCardFooter>
        </AppCard>

        <AppCard className="lg:col-span-7">

          <AppCardHeader>
            <h2 className="text-lg font-semibold text-text">{t("config.prefixTitle")}</h2>
            <p className="text-sm text-muted">{t("config.prefixDesc")}</p>
          </AppCardHeader>
          <AppCardBody>
            <FormField
              label={t("config.prefixLabel")}
            >
              <AppInput
                aria-label={t("config.prefixAria")}
                placeholder={t("config.prefixPlaceholder")}
                value={messagePrefix}
                onValueChange={setMessagePrefix}
              />
            </FormField>
          </AppCardBody>
          <AppCardFooter>
            <AppButton
              color="primary"
              isLoading={savingPrefix}
              startContent={<Save className="h-4 w-4" />}
              onPress={savePrefix}
            >
              {t("config.saveBasic")}
            </AppButton>
          </AppCardFooter>
        </AppCard>

        <AppCard className="lg:col-span-12">
          <AppCardHeader>
            <h2 className="text-lg font-semibold text-text">{t("config.triggerLogTitle")}</h2>
            <p className="text-sm text-muted">{t("config.triggerLogDesc")}</p>
          </AppCardHeader>
          <AppCardBody>
            <div className="space-y-4">
              <FormField
                description={t("config.triggerLogEnabledDesc")}
                label={t("config.triggerLogEnabledLabel")}
              >
                <Switch
                  isSelected={triggerLogEnabled}
                  onValueChange={setTriggerLogEnabled}
                  aria-label={t("config.triggerLogEnabledAria")}
                >
                  {triggerLogEnabled ? t("config.enabled") : t("config.disabled")}
                </Switch>
              </FormField>

              <FormField
                description={t("config.triggerLogDatabaseDesc")}
                label={t("config.triggerLogDatabaseLabel")}
              >
                <AppInput
                  aria-label={t("config.triggerLogDatabaseAria")}
                  placeholder="postgres://user:pass@localhost:5432/nyanyabot?sslmode=disable"
                  value={triggerLogDatabaseURI}
                  onValueChange={setTriggerLogDatabaseURI}
                />
              </FormField>

              <div className="grid gap-4 sm:grid-cols-3">
                <FormField
                  description={t("config.triggerLogQueueSizeDesc")}
                  label={t("config.triggerLogQueueSizeLabel")}
                >
                  <AppInput
                    aria-label={t("config.triggerLogQueueSizeAria")}
                    placeholder="1000"
                    type="number"
                    value={triggerLogQueueSize}
                    onValueChange={setTriggerLogQueueSize}
                  />
                </FormField>

                <FormField
                  description={t("config.triggerLogBatchSizeDesc")}
                  label={t("config.triggerLogBatchSizeLabel")}
                >
                  <AppInput
                    aria-label={t("config.triggerLogBatchSizeAria")}
                    placeholder="100"
                    type="number"
                    value={triggerLogBatchSize}
                    onValueChange={setTriggerLogBatchSize}
                  />
                </FormField>

                <FormField
                  description={t("config.triggerLogBatchIntervalDesc")}
                  label={t("config.triggerLogBatchIntervalLabel")}
                >
                  <AppInput
                    aria-label={t("config.triggerLogBatchIntervalAria")}
                    placeholder="5s"
                    value={triggerLogBatchInterval}
                    onValueChange={setTriggerLogBatchInterval}
                  />
                </FormField>
              </div>
            </div>
          </AppCardBody>
          <AppCardFooter>
            <AppButton
              color="primary"
              isLoading={savingTriggerLog}
              startContent={<Save className="h-4 w-4" />}
              onPress={saveTriggerLog}
            >
              {t("config.saveTriggerLog")}
            </AppButton>
          </AppCardFooter>
        </AppCard>

        <AppCard className="lg:col-span-7">
          <AppCardHeader>
            <h2 className="text-lg font-semibold text-text">{t("config.globalsTitle")}</h2>
          </AppCardHeader>
          <AppCardBody>
            <div className="space-y-3">
              {globalsRows.map((row, index) => (
                <div key={row.id} className="grid gap-2 rounded-lg border border-border/70 bg-surface-elevated/50 p-3 sm:grid-cols-[1fr_1fr_auto]">
                  <AppInput
                    aria-label={t("config.globalKeyAria", { index: index + 1 })}
                    placeholder={t("config.globalKeyPlaceholder")}
                    value={row.key}
                    onValueChange={(value) => updateGlobalRow(row.id, { key: value })}
                  />
                  <AppInput
                    aria-label={t("config.globalValueAria", { index: index + 1 })}
                    placeholder={t("config.globalValuePlaceholder")}
                    value={row.value}
                    onValueChange={(value) => updateGlobalRow(row.id, { value })}
                  />
                  <AppButton
                    aria-label={t("config.globalDeleteAria", { index: index + 1 })}
                    isIconOnly
                    tone="ghost"
                    onPress={() => removeGlobalRow(row.id)}
                  >
                    <Trash2 className="h-4 w-4" />
                  </AppButton>
                </div>
              ))}
            </div>

            <Divider className="my-1 bg-border/70" />

            <div className="flex flex-wrap items-center gap-2">
              <AppButton startContent={<Plus className="h-4 w-4" />} tone="neutral" onPress={() => setGlobalsRows((rows) => [...rows, createRow()])}>
                {t("config.globalAdd")}
              </AppButton>
              <p className="text-xs text-muted">{t("config.globalCount", { count: Object.keys(globalsPayload).length })}</p>
            </div>
          </AppCardBody>
          <AppCardFooter>
            <AppButton
              color="primary"
              isLoading={savingGlobals}
              startContent={<Save className="h-4 w-4" />}
              onPress={saveGlobals}
            >
              {t("config.saveGlobals")}
            </AppButton>
          </AppCardFooter>
        </AppCard>
      </div>
    </section>
  );
}
