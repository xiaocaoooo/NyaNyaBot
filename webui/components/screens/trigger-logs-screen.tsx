"use client";

import { Chip, Divider, Spinner } from "@heroui/react";
import { ChevronDown, ChevronUp, RefreshCw, Search } from "lucide-react";
import { useCallback, useEffect, useState } from "react";

import { AppButton } from "@/components/ui/button";
import { useI18n } from "@/components/providers/i18n-provider";
import { AppCard, AppCardBody, AppCardHeader } from "@/components/ui/card";
import { AppInput } from "@/components/ui/input";
import { StatusMessage } from "@/components/ui/status-message";
import { useAutoRefresh } from "@/lib/hooks/use-auto-refresh";
import { apiClient } from "@/lib/api/client";
import type { TriggerLog, TriggerLogQuery, TriggerLogsResponse } from "@/lib/api/types";

function StatItem({ label, value }: { label: string; value: string | number }) {
  return (
    <div className="rounded-lg border border-border/70 bg-surface-elevated/70 p-3">
      <p className="text-xs text-muted">{label}</p>
      <p className="mt-1 text-lg font-semibold text-text">{value}</p>
    </div>
  );
}

interface TriggerLogItemProps {
  log: TriggerLog;
}

function TriggerLogItem({ log }: TriggerLogItemProps) {
  const { t } = useI18n();
  const [expanded, setExpanded] = useState(false);

  const statusColor = log.success ? "success" : "danger";
  const statusText = log.success ? t("triggerLogs.statusSuccess") : t("triggerLogs.statusFailed");

  return (
    <li className="rounded-lg border border-border/70 bg-surface-elevated/50 p-3">
      <div className="space-y-2">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div className="flex items-center gap-2">
            <Chip color={statusColor} radius="sm" size="sm" variant="dot">
              {statusText}
            </Chip>
            <p className="font-medium text-text">
              {log.plugin_id} / {log.listener_id}
            </p>
          </div>
          <div className="flex items-center gap-2">
            <Chip radius="sm" size="sm" variant="flat">
              {log.listener_type}
            </Chip>
            <Chip radius="sm" size="sm" variant="flat">
              {log.duration_ms}ms
            </Chip>
          </div>
        </div>

        <div className="flex flex-wrap gap-2 text-xs text-muted">
          <span>{t("triggerLogs.triggeredAt")}: {new Date(log.triggered_at).toLocaleString()}</span>
          <span>·</span>
          <span>{t("triggerLogs.groupId")}: {log.group_id}</span>
          <span>·</span>
          <span>{t("triggerLogs.userId")}: {log.user_id}</span>
          {log.message_seq && (
            <>
              <span>·</span>
              <span>{t("triggerLogs.messageSeq")}: {log.message_seq}</span>
            </>
          )}
        </div>

        {!log.success && log.error_message && (
          <div className="rounded-md bg-danger/10 p-2">
            <p className="text-xs text-danger">{t("triggerLogs.errorMessage")}: {log.error_message}</p>
          </div>
        )}

        <div className="flex items-center justify-between">
          <p className="text-xs text-muted">
            {t("triggerLogs.traceId")}: {log.trace_id}
          </p>
          <AppButton
            size="sm"
            startContent={expanded ? <ChevronUp className="h-3 w-3" /> : <ChevronDown className="h-3 w-3" />}
            tone="ghost"
            onPress={() => setExpanded(!expanded)}
          >
            {expanded ? t("triggerLogs.hideDetails") : t("triggerLogs.showDetails")}
          </AppButton>
        </div>

        {expanded && (
          <>
            <Divider className="my-2 bg-border/70" />
            <div className="space-y-2">
              <div>
                <p className="text-xs font-medium text-muted">{t("triggerLogs.selfId")}:</p>
                <p className="text-sm text-text">{log.self_id}</p>
              </div>
              {log.message_id > 0 && (
                <div>
                  <p className="text-xs font-medium text-muted">{t("triggerLogs.messageId")}:</p>
                  <p className="text-sm text-text">{log.message_id}</p>
                </div>
              )}
              <div>
                <p className="text-xs font-medium text-muted">{t("triggerLogs.recordedAt")}:</p>
                <p className="text-sm text-text">{new Date(log.recorded_at).toLocaleString()}</p>
              </div>
              <div>
                <p className="text-xs font-medium text-muted">{t("triggerLogs.triggerData")}:</p>
                <pre className="mt-1 overflow-x-auto rounded-md bg-surface p-2 text-xs text-text">
                  {JSON.stringify(log.trigger_data, null, 2)}
                </pre>
              </div>
            </div>
          </>
        )}
      </div>
    </li>
  );
}

export function TriggerLogsScreen() {
  const { t } = useI18n();
  const [query, setQuery] = useState<TriggerLogQuery>({
    page: 1,
    page_size: 20,
    sort_by: "triggered_at",
    sort_desc: true,
  });
  const [response, setResponse] = useState<TriggerLogsResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [showAdvanced, setShowAdvanced] = useState(false);

  // 表单字段状态
  const [groupId, setGroupId] = useState("");
  const [userId, setUserId] = useState("");
  const [pluginId, setPluginId] = useState("");
  const [listenerId, setListenerId] = useState("");
  const [startTime, setStartTime] = useState("");
  const [endTime, setEndTime] = useState("");
  const [messageSeq, setMessageSeq] = useState("");
  const [listenerType, setListenerType] = useState<"" | "command" | "event" | "cron">("");
  const [sortBy, setSortBy] = useState<"triggered_at" | "duration_ms">("triggered_at");
  const [pageSize, setPageSize] = useState("20");

  const fetchData = useCallback(async (silent = false) => {
    if (!silent) setLoading(true);
    setError(null);

    try {
      const queryParams: TriggerLogQuery = {
        page: query.page,
        page_size: query.page_size,
        sort_by: query.sort_by,
        sort_desc: query.sort_desc,
      };

      if (groupId) queryParams.group_id = parseInt(groupId, 10);
      if (userId) queryParams.user_id = parseInt(userId, 10);
      if (pluginId) queryParams.plugin_id = pluginId;
      if (listenerId) queryParams.listener_id = listenerId;
      if (startTime) queryParams.start_time = startTime;
      if (endTime) queryParams.end_time = endTime;
      if (messageSeq) queryParams.message_seq = messageSeq;
      if (listenerType) queryParams.listener_type = listenerType;

      const data = await apiClient.queryTriggerLogs(queryParams);
      setResponse(data);
    } catch (err) {
      setError(err instanceof Error ? err.message : t("triggerLogs.errorLoad"));
    } finally {
      if (!silent) setLoading(false);
    }
  }, [query, groupId, userId, pluginId, listenerId, startTime, endTime, messageSeq, listenerType, t]);

  useAutoRefresh(useCallback(() => fetchData(true), [fetchData]));

  useEffect(() => {
    void fetchData();
  }, [fetchData]);

  const handleSearch = () => {
    setQuery((prev) => ({
      ...prev,
      page: 1,
      sort_by: sortBy,
      page_size: parseInt(pageSize, 10) || 20,
    }));
  };

  const handlePageChange = (newPage: number) => {
    setQuery((prev) => ({ ...prev, page: newPage }));
  };

  const totalPages = response ? Math.ceil(response.total / response.page_size) : 0;

  return (
    <section className="space-y-6">
      <header className="space-y-2">
        <h1 className="text-2xl font-semibold text-text sm:text-3xl">{t("triggerLogs.title")}</h1>
        <p className="max-w-2xl text-sm text-muted sm:text-base">{t("triggerLogs.subtitle")}</p>
      </header>

      <AppCard>
        <AppCardHeader>
          <h2 className="text-lg font-semibold text-text">{t("triggerLogs.queryFormTitle")}</h2>
          <p className="text-sm text-muted">{t("triggerLogs.queryFormDesc")}</p>
        </AppCardHeader>
        <AppCardBody>
          <div className="space-y-4">
            <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
              <AppInput
                label={t("triggerLogs.groupIdLabel")}
                placeholder={t("triggerLogs.groupIdPlaceholder")}
                type="number"
                value={groupId}
                onChange={(e) => setGroupId(e.target.value)}
              />
              <AppInput
                label={t("triggerLogs.userIdLabel")}
                placeholder={t("triggerLogs.userIdPlaceholder")}
                type="number"
                value={userId}
                onChange={(e) => setUserId(e.target.value)}
              />
              <AppInput
                label={t("triggerLogs.pluginIdLabel")}
                placeholder={t("triggerLogs.pluginIdPlaceholder")}
                value={pluginId}
                onChange={(e) => setPluginId(e.target.value)}
              />
              <AppInput
                label={t("triggerLogs.listenerIdLabel")}
                placeholder={t("triggerLogs.listenerIdPlaceholder")}
                value={listenerId}
                onChange={(e) => setListenerId(e.target.value)}
              />
              <AppInput
                label={t("triggerLogs.startTimeLabel")}
                placeholder={t("triggerLogs.startTimePlaceholder")}
                type="datetime-local"
                value={startTime}
                onChange={(e) => setStartTime(e.target.value)}
              />
              <AppInput
                label={t("triggerLogs.endTimeLabel")}
                placeholder={t("triggerLogs.endTimePlaceholder")}
                type="datetime-local"
                value={endTime}
                onChange={(e) => setEndTime(e.target.value)}
              />
            </div>

            <div className="flex items-center gap-2">
              <AppButton
                size="sm"
                tone="ghost"
                onPress={() => setShowAdvanced(!showAdvanced)}
              >
                {showAdvanced ? t("triggerLogs.hideAdvanced") : t("triggerLogs.showAdvanced")}
              </AppButton>
            </div>

            {showAdvanced && (
              <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
                <AppInput
                  label={t("triggerLogs.messageSeqLabel")}
                  placeholder={t("triggerLogs.messageSeqPlaceholder")}
                  value={messageSeq}
                  onChange={(e) => setMessageSeq(e.target.value)}
                />
                <div className="space-y-2">
                  <label className="text-sm text-muted">{t("triggerLogs.listenerTypeLabel")}</label>
                  <select
                    className="w-full rounded-md border border-border/70 bg-surface px-3 py-2 text-sm text-text"
                    value={listenerType}
                    onChange={(e) => setListenerType(e.target.value as "" | "command" | "event" | "cron")}
                  >
                    <option value="">{t("triggerLogs.listenerTypeAll")}</option>
                    <option value="command">{t("triggerLogs.listenerTypeCommand")}</option>
                    <option value="event">{t("triggerLogs.listenerTypeEvent")}</option>
                    <option value="cron">{t("triggerLogs.listenerTypeCron")}</option>
                  </select>
                </div>
                <div className="space-y-2">
                  <label className="text-sm text-muted">{t("triggerLogs.sortByLabel")}</label>
                  <select
                    className="w-full rounded-md border border-border/70 bg-surface px-3 py-2 text-sm text-text"
                    value={sortBy}
                    onChange={(e) => setSortBy(e.target.value as "triggered_at" | "duration_ms")}
                  >
                    <option value="triggered_at">{t("triggerLogs.sortByTime")}</option>
                    <option value="duration_ms">{t("triggerLogs.sortByDuration")}</option>
                  </select>
                </div>
                <AppInput
                  label={t("triggerLogs.pageSizeLabel")}
                  placeholder="20"
                  type="number"
                  value={pageSize}
                  onChange={(e) => setPageSize(e.target.value)}
                />
              </div>
            )}

            <div className="flex flex-wrap items-center gap-3">
              <AppButton startContent={<Search className="h-4 w-4" />} tone="primary" onPress={handleSearch}>
                {t("triggerLogs.search")}
              </AppButton>
              <AppButton startContent={<RefreshCw className="h-4 w-4" />} tone="neutral" onPress={fetchData}>
                {t("triggerLogs.refresh")}
              </AppButton>
              {error ? <StatusMessage tone="error">{error}</StatusMessage> : null}
            </div>
          </div>
        </AppCardBody>
      </AppCard>

      {loading ? (
        <div className="flex min-h-[260px] items-center justify-center">
          <Spinner color="primary" label={t("triggerLogs.loading")} labelColor="primary" />
        </div>
      ) : response ? (
        <div className="space-y-4">
          <AppCard>
            <AppCardHeader>
              <h2 className="text-lg font-semibold text-text">{t("triggerLogs.statsTitle")}</h2>
              <p className="text-sm text-muted">{t("triggerLogs.statsDesc")}</p>
            </AppCardHeader>
            <AppCardBody>
              <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
                <StatItem label={t("triggerLogs.statTotal")} value={response.stats.total_count} />
                <StatItem label={t("triggerLogs.statSuccess")} value={response.stats.success_count} />
                <StatItem label={t("triggerLogs.statFailed")} value={response.stats.failed_count} />
                <StatItem
                  label={t("triggerLogs.statAvgDuration")}
                  value={`${response.stats.avg_duration_ms.toFixed(2)}ms`}
                />
              </div>
            </AppCardBody>
          </AppCard>

          <AppCard>
            <AppCardHeader>
              <h2 className="text-lg font-semibold text-text">{t("triggerLogs.resultsTitle")}</h2>
              <p className="text-sm text-muted">
                {t("triggerLogs.resultsDesc", {
                  total: response.total,
                  page: response.page,
                  totalPages,
                })}
              </p>
            </AppCardHeader>
            <AppCardBody>
              {response.records.length === 0 ? (
                <p className="rounded-lg border border-dashed border-border p-4 text-sm text-muted">
                  {t("triggerLogs.noResults")}
                </p>
              ) : (
                <ul className="space-y-3">
                  {response.records.map((log) => (
                    <TriggerLogItem key={log.trace_id} log={log} />
                  ))}
                </ul>
              )}

              {totalPages > 1 && (
                <>
                  <Divider className="my-4 bg-border/70" />
                  <div className="flex flex-wrap items-center justify-between gap-3">
                    <p className="text-sm text-muted">
                      {t("triggerLogs.pageInfo", {
                        current: response.page,
                        total: totalPages,
                      })}
                    </p>
                    <div className="flex gap-2">
                      <AppButton
                        isDisabled={response.page <= 1}
                        size="sm"
                        tone="neutral"
                        onPress={() => handlePageChange(response.page - 1)}
                      >
                        {t("triggerLogs.prevPage")}
                      </AppButton>
                      <AppButton
                        isDisabled={response.page >= totalPages}
                        size="sm"
                        tone="neutral"
                        onPress={() => handlePageChange(response.page + 1)}
                      >
                        {t("triggerLogs.nextPage")}
                      </AppButton>
                    </div>
                  </div>
                </>
              )}
            </AppCardBody>
          </AppCard>
        </div>
      ) : null}
    </section>
  );
}
