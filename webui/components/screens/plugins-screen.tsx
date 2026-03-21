"use client";

import { Chip, Divider, Spinner, Switch, Tab, Tabs } from "@heroui/react";
import { RefreshCw, Save } from "lucide-react";
import { type Key, useCallback, useEffect, useMemo, useState } from "react";

import { AppButton } from "@/components/ui/button";
import { type TranslateFn, useI18n } from "@/components/providers/i18n-provider";
import { AppCard, AppCardBody, AppCardFooter, AppCardHeader } from "@/components/ui/card";
import { AppInput } from "@/components/ui/input";
import { StatusMessage } from "@/components/ui/status-message";
import { AppTextarea } from "@/components/ui/textarea";
import { apiClient } from "@/lib/api/client";
import type { PluginDescriptor } from "@/lib/api/types";

type EditorMode = "schema" | "json";
type JSONSchema = {
  additionalProperties?: boolean | JSONSchema;
  default?: unknown;
  description?: string;
  enum?: unknown[];
  items?: JSONSchema;
  maxLength?: number;
  maximum?: number;
  minLength?: number;
  minimum?: number;
  pattern?: string;
  properties?: Record<string, JSONSchema>;
  required?: string[];
  title?: string;
  type?: string | string[];
};

function formatJSON(input: unknown): string {
  return JSON.stringify(input, null, 2);
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function normalizeSchema(value: unknown): JSONSchema | null {
  return isRecord(value) ? (value as JSONSchema) : null;
}

function normalizeConfigObject(value: unknown): Record<string, unknown> {
  return isRecord(value) ? (value as Record<string, unknown>) : {};
}

function cloneObject(value: Record<string, unknown>): Record<string, unknown> {
  return JSON.parse(JSON.stringify(value)) as Record<string, unknown>;
}

function deepMerge(base: Record<string, unknown>, override: Record<string, unknown>): Record<string, unknown> {
  const output = cloneObject(base);

  for (const [key, value] of Object.entries(override)) {
    if (isRecord(value) && isRecord(output[key])) {
      output[key] = deepMerge(output[key] as Record<string, unknown>, value);
      continue;
    }
    output[key] = value;
  }

  return output;
}

function getSchemaType(schema: JSONSchema): string | undefined {
  if (Array.isArray(schema.type)) {
    return schema.type.find((item) => item !== "null");
  }
  return schema.type;
}

function getValueAtPath(source: Record<string, unknown>, path: string[]): unknown {
  let current: unknown = source;

  for (const segment of path) {
    if (!isRecord(current)) {
      return undefined;
    }
    current = current[segment];
  }

  return current;
}

function setValueAtPath(source: Record<string, unknown>, path: string[], value: unknown): Record<string, unknown> {
  const next = cloneObject(source);
  let cursor: Record<string, unknown> = next;

  for (let index = 0; index < path.length - 1; index += 1) {
    const segment = path[index];
    const child = cursor[segment];
    if (!isRecord(child)) {
      cursor[segment] = {};
    }
    cursor = cursor[segment] as Record<string, unknown>;
  }

  const leaf = path[path.length - 1];
  if (value === undefined) {
    delete cursor[leaf];
  } else {
    cursor[leaf] = value;
  }

  return next;
}

function asPath(basePath: string, key: string): string {
  return basePath === "$" ? `$.${key}` : `${basePath}.${key}`;
}

function validateBySchema(value: unknown, schema: JSONSchema, t: TranslateFn, path = "$"): string[] {
  const errors: string[] = [];
  const schemaType = getSchemaType(schema) ?? (schema.properties ? "object" : undefined);

  if (Array.isArray(schema.enum)) {
    const matched = schema.enum.some((candidate) => JSON.stringify(candidate) === JSON.stringify(value));
    if (!matched) {
      errors.push(t("plugins.validation.enum", { path }));
      return errors;
    }
  }

  if (!schemaType) {
    return errors;
  }

  switch (schemaType) {
    case "object": {
      if (!isRecord(value)) {
        errors.push(t("plugins.validation.object", { path }));
        return errors;
      }

      const obj = value as Record<string, unknown>;
      const requiredKeys = Array.isArray(schema.required) ? schema.required : [];
      for (const requiredKey of requiredKeys) {
        if (obj[requiredKey] === undefined) {
          errors.push(t("plugins.validation.required", { path: asPath(path, requiredKey) }));
        }
      }

      const properties = schema.properties ?? {};
      for (const [key, childSchema] of Object.entries(properties)) {
        if (obj[key] === undefined) {
          continue;
        }
        errors.push(...validateBySchema(obj[key], childSchema, t, asPath(path, key)));
      }

      if (schema.additionalProperties === false) {
        for (const key of Object.keys(obj)) {
          if (!(key in properties)) {
            errors.push(t("plugins.validation.additional", { path: asPath(path, key) }));
          }
        }
      }
      if (isRecord(schema.additionalProperties)) {
        for (const [key, extraValue] of Object.entries(obj)) {
          if (key in properties) {
            continue;
          }
          errors.push(...validateBySchema(extraValue, schema.additionalProperties, t, asPath(path, key)));
        }
      }
      break;
    }

    case "array": {
      if (!Array.isArray(value)) {
        errors.push(t("plugins.validation.array", { path }));
        return errors;
      }

      if (schema.items) {
        value.forEach((item, index) => {
          errors.push(...validateBySchema(item, schema.items as JSONSchema, t, `${path}[${index}]`));
        });
      }
      break;
    }

    case "string": {
      if (typeof value !== "string") {
        errors.push(t("plugins.validation.string", { path }));
        return errors;
      }
      if (typeof schema.minLength === "number" && value.length < schema.minLength) {
        errors.push(t("plugins.validation.minLength", { path, value: schema.minLength }));
      }
      if (typeof schema.maxLength === "number" && value.length > schema.maxLength) {
        errors.push(t("plugins.validation.maxLength", { path, value: schema.maxLength }));
      }
      if (schema.pattern) {
        try {
          const reg = new RegExp(schema.pattern);
          if (!reg.test(value)) {
            errors.push(t("plugins.validation.pattern", { path, pattern: schema.pattern }));
          }
        } catch {
          errors.push(t("plugins.validation.patternInvalid", { path }));
        }
      }
      break;
    }

    case "number": {
      if (typeof value !== "number" || Number.isNaN(value)) {
        errors.push(t("plugins.validation.number", { path }));
        return errors;
      }
      if (typeof schema.minimum === "number" && value < schema.minimum) {
        errors.push(t("plugins.validation.min", { path, value: schema.minimum }));
      }
      if (typeof schema.maximum === "number" && value > schema.maximum) {
        errors.push(t("plugins.validation.max", { path, value: schema.maximum }));
      }
      break;
    }

    case "integer": {
      if (typeof value !== "number" || Number.isNaN(value) || !Number.isInteger(value)) {
        errors.push(t("plugins.validation.integer", { path }));
        return errors;
      }
      if (typeof schema.minimum === "number" && value < schema.minimum) {
        errors.push(t("plugins.validation.min", { path, value: schema.minimum }));
      }
      if (typeof schema.maximum === "number" && value > schema.maximum) {
        errors.push(t("plugins.validation.max", { path, value: schema.maximum }));
      }
      break;
    }

    case "boolean": {
      if (typeof value !== "boolean") {
        errors.push(t("plugins.validation.boolean", { path }));
      }
      break;
    }

    default:
      break;
  }

  return errors;
}

interface SchemaFieldListProps {
  pathPrefix: string[];
  schema: JSONSchema;
  value: Record<string, unknown>;
  t: TranslateFn;
  onChange: (path: string[], nextValue: unknown) => void;
}

function SchemaFieldList({ onChange, pathPrefix, schema, t, value }: SchemaFieldListProps) {
  const properties = schema.properties ?? {};
  const requiredSet = new Set(schema.required ?? []);

  return (
    <div className="space-y-3">
      {Object.entries(properties).map(([key, childSchema]) => {
        const path = [...pathPrefix, key];
        const currentValue = getValueAtPath(value, path);
        const typedSchema = normalizeSchema(childSchema) ?? {};
        const schemaType = getSchemaType(typedSchema) ?? (typedSchema.properties ? "object" : undefined);
        const title = typedSchema.title || key;
        const description = typedSchema.description;
        const required = requiredSet.has(key);

        if (schemaType === "object" && typedSchema.properties) {
          return (
            <div key={path.join(".")} className="space-y-3 rounded-lg border border-border/70 bg-surface-elevated/50 p-3">
              <div>
                <p className="text-sm font-medium text-text">
                  {title}
                  {required ? <span className="text-danger"> *</span> : null}
                </p>
                {description ? <p className="text-xs text-muted">{description}</p> : null}
              </div>
              <SchemaFieldList onChange={onChange} pathPrefix={path} schema={typedSchema} t={t} value={value} />
            </div>
          );
        }

        if (Array.isArray(typedSchema.enum) && typedSchema.enum.length > 0) {
          const selectValue = currentValue === undefined ? "" : String(currentValue);

          return (
            <div key={path.join(".")} className="space-y-1.5">
              <p className="text-sm font-medium text-text">
                {title}
                {required ? <span className="text-danger"> *</span> : null}
              </p>
              {description ? <p className="text-xs text-muted">{description}</p> : null}
              <select
                className="h-10 w-full rounded-md border border-border/70 bg-surface px-3 text-sm text-text focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary"
                value={selectValue}
                onChange={(event) => {
                  const next = event.target.value;
                  onChange(path, next === "" ? undefined : next);
                }}
              >
                <option value="">{t("plugins.schemaSelectPlaceholder")}</option>
                {typedSchema.enum.map((option) => {
                  const stringified = String(option);
                  return (
                    <option key={stringified} value={stringified}>
                      {stringified}
                    </option>
                  );
                })}
              </select>
            </div>
          );
        }

        if (schemaType === "boolean") {
          const boolValue = typeof currentValue === "boolean" ? currentValue : Boolean(typedSchema.default ?? false);

          return (
            <div key={path.join(".")} className="space-y-2 rounded-lg border border-border/70 bg-surface-elevated/40 p-3">
              <div>
                <p className="text-sm font-medium text-text">
                  {title}
                  {required ? <span className="text-danger"> *</span> : null}
                </p>
                {description ? <p className="text-xs text-muted">{description}</p> : null}
              </div>
              <Switch isSelected={boolValue} size="sm" onValueChange={(next) => onChange(path, next)}>
                {boolValue ? t("plugins.schemaEnabled") : t("plugins.schemaDisabled")}
              </Switch>
            </div>
          );
        }

        if (schemaType === "number" || schemaType === "integer") {
          const numberValue = typeof currentValue === "number" ? String(currentValue) : "";

          return (
            <div key={path.join(".")} className="space-y-1.5">
              <p className="text-sm font-medium text-text">
                {title}
                {required ? <span className="text-danger"> *</span> : null}
              </p>
              {description ? <p className="text-xs text-muted">{description}</p> : null}
              <AppInput
                aria-label={title}
                placeholder={schemaType === "integer" ? t("plugins.schemaInputInteger") : t("plugins.schemaInputNumber")}
                type="number"
                value={numberValue}
                onValueChange={(next) => {
                  if (next.trim() === "") {
                    onChange(path, undefined);
                    return;
                  }
                  const num = schemaType === "integer" ? Number.parseInt(next, 10) : Number(next);
                  if (Number.isNaN(num)) {
                    return;
                  }
                  onChange(path, num);
                }}
              />
            </div>
          );
        }

        if (schemaType === "string") {
          const stringValue = typeof currentValue === "string" ? currentValue : "";

          return (
            <div key={path.join(".")} className="space-y-1.5">
              <p className="text-sm font-medium text-text">
                {title}
                {required ? <span className="text-danger"> *</span> : null}
              </p>
              {description ? <p className="text-xs text-muted">{description}</p> : null}
              <AppInput
                aria-label={title}
                placeholder={typedSchema.pattern ? t("plugins.schemaPatternPrefix", { pattern: typedSchema.pattern }) : t("plugins.schemaInputText")}
                value={stringValue}
                onValueChange={(next) => onChange(path, next === "" ? undefined : next)}
              />
            </div>
          );
        }

        const serializedValue = currentValue === undefined ? "" : formatJSON(currentValue);

        return (
          <div key={path.join(".")} className="space-y-1.5">
            <p className="text-sm font-medium text-text">
              {title}
              {required ? <span className="text-danger"> *</span> : null}
            </p>
            {description ? <p className="text-xs text-muted">{description}</p> : null}
            <AppTextarea
              aria-label={t("plugins.schemaJsonAria", { title })}
              classNames={{
                input: "font-mono text-xs leading-6",
              }}
              minRows={4}
              placeholder={t("plugins.schemaJsonPlaceholder")}
              value={serializedValue}
              onValueChange={(next) => {
                if (next.trim() === "") {
                  onChange(path, undefined);
                  return;
                }
                try {
                  onChange(path, JSON.parse(next));
                } catch {
                  // Keep last valid state and let user use JSON mode for complex raw editing.
                }
              }}
            />
          </div>
        );
      })}
    </div>
  );
}

function GlobalVariableUsageHint({ t }: { t: TranslateFn }) {
  return (
    <div className="rounded-lg border border-primary/30 bg-primary/10 p-4">
      <p className="text-sm font-semibold text-text">{t("plugins.globalHintTitle")}</p>
      <div className="mt-2 space-y-2 text-xs leading-6 text-muted">
        <p>{t("plugins.globalHintLine1")}</p>
        <p>{t("plugins.globalHintLine2")}</p>
        <p>{t("plugins.globalHintLine3")}</p>
      </div>
    </div>
  );
}

export function PluginsScreen() {
  const { t } = useI18n();
  const [plugins, setPlugins] = useState<PluginDescriptor[]>([]);
  const [selectedId, setSelectedId] = useState<string>("");
  const [pluginConfigText, setPluginConfigText] = useState("{}");
  const [schemaConfig, setSchemaConfig] = useState<Record<string, unknown>>({});
  const [editorMode, setEditorMode] = useState<EditorMode>("json");

  const [loading, setLoading] = useState(true);
  const [loadingConfig, setLoadingConfig] = useState(false);
  const [savingConfig, setSavingConfig] = useState(false);

  const [error, setError] = useState<string | null>(null);
  const [status, setStatus] = useState<string | null>(null);

  const selectedPlugin = useMemo(
    () => plugins.find((plugin) => plugin.plugin_id === selectedId) ?? null,
    [plugins, selectedId],
  );

  const schemaObject = useMemo(() => normalizeSchema(selectedPlugin?.config?.schema), [selectedPlugin]);
  const canUseSchemaEditor = useMemo(
    () => Boolean(schemaObject && schemaObject.properties && Object.keys(schemaObject.properties).length > 0),
    [schemaObject],
  );

  const loadPlugins = useCallback(async () => {
    setLoading(true);
    setError(null);

    try {
      const data = await apiClient.fetchPlugins();
      setPlugins(data);
      if (data.length > 0) {
        setSelectedId((current) => current || data[0].plugin_id);
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : t("plugins.errorLoadList"));
    } finally {
      setLoading(false);
    }
  }, [t]);

  useEffect(() => {
    void loadPlugins();
  }, [loadPlugins]);

  const loadPluginConfig = useCallback(async (plugin: PluginDescriptor) => {
    setLoadingConfig(true);
    setError(null);

    try {
      const response = await apiClient.fetchPluginConfig(plugin.plugin_id);
      const defaults = normalizeConfigObject(plugin.config?.default);
      const currentConfig = normalizeConfigObject(response.config);
      const mergedConfig = deepMerge(defaults, currentConfig);

      setSchemaConfig(mergedConfig);
      setPluginConfigText(formatJSON(mergedConfig));
      setEditorMode(canUseSchemaEditor ? "schema" : "json");
    } catch (err) {
      setError(err instanceof Error ? err.message : t("plugins.errorLoadConfig"));
      setSchemaConfig({});
      setPluginConfigText("{}");
    } finally {
      setLoadingConfig(false);
    }
  }, [canUseSchemaEditor, t]);

  useEffect(() => {
    if (!selectedPlugin?.config) {
      setSchemaConfig({});
      setPluginConfigText("{}");
      setEditorMode("json");
      return;
    }

    void loadPluginConfig(selectedPlugin);
  }, [loadPluginConfig, selectedPlugin]);

  const handleModeChange = (key: Key) => {
    const nextMode = String(key) as EditorMode;
    if (nextMode === "schema") {
      try {
        const parsed = JSON.parse(pluginConfigText) as unknown;
        if (isRecord(parsed)) {
          setSchemaConfig(parsed);
        }
      } catch {
        // keep existing schema state
      }
    }
    if (nextMode === "json") {
      setPluginConfigText(formatJSON(schemaConfig));
    }
    setEditorMode(nextMode);
  };

  const handleSchemaFieldChange = (path: string[], nextValue: unknown) => {
    setSchemaConfig((current) => {
      const updated = setValueAtPath(current, path, nextValue);
      setPluginConfigText(formatJSON(updated));
      return updated;
    });
  };

  const savePluginConfig = async () => {
    if (!selectedPlugin) {
      return;
    }

    setSavingConfig(true);
    setError(null);
    setStatus(null);

    try {
      let parsedConfig: Record<string, unknown>;

      if (editorMode === "schema") {
        parsedConfig = schemaConfig;
      } else {
        const parsed = JSON.parse(pluginConfigText) as unknown;
        if (!isRecord(parsed)) {
          throw new Error(t("plugins.errorConfigMustObject"));
        }
        parsedConfig = parsed;
      }

      if (schemaObject) {
        const validationErrors = validateBySchema(parsedConfig, schemaObject, t);
        if (validationErrors.length > 0) {
          throw new Error(t("plugins.validationFailed", { error: validationErrors[0] }));
        }
      }

      await apiClient.updatePluginConfig(selectedPlugin.plugin_id, {
        config: parsedConfig,
      });

      setSchemaConfig(parsedConfig);
      setPluginConfigText(formatJSON(parsedConfig));
      setStatus(t("plugins.statusSaved"));
    } catch (err) {
      setError(err instanceof Error ? err.message : t("plugins.errorSaveConfig"));
    } finally {
      setSavingConfig(false);
    }
  };

  return (
    <section className="space-y-6">
      <header className="space-y-2">
        <h1 className="text-2xl font-semibold text-text sm:text-3xl">{t("plugins.title")}</h1>
        <p className="max-w-3xl text-sm text-muted sm:text-base">{t("plugins.subtitle")}</p>
      </header>

      <div className="flex flex-wrap items-center gap-3">
        <AppButton startContent={<RefreshCw className="h-4 w-4" />} tone="neutral" onPress={loadPlugins}>
          {t("plugins.refresh")}
        </AppButton>
        {status ? <StatusMessage tone="success">{status}</StatusMessage> : null}
        {error ? <StatusMessage tone="error">{error}</StatusMessage> : null}
      </div>

      {loading ? (
        <div className="flex min-h-[260px] items-center justify-center">
          <Spinner color="primary" label={t("plugins.loadingList")} labelColor="primary" />
        </div>
      ) : (
        <div className="grid gap-4 lg:grid-cols-12">
          <AppCard className="lg:col-span-4">
            <AppCardHeader>
              <h2 className="text-lg font-semibold text-text">{t("plugins.listTitle")}</h2>
              <p className="text-sm text-muted">{t("plugins.listCount", { count: plugins.length })}</p>
            </AppCardHeader>
            <AppCardBody>
              {plugins.length === 0 ? (
                <p className="rounded-lg border border-dashed border-border p-4 text-sm text-muted">{t("plugins.listEmpty")}</p>
              ) : (
                <ul className="space-y-2" aria-label={t("plugins.listAria")} role="listbox">
                  {plugins.map((plugin) => {
                    const active = plugin.plugin_id === selectedId;
                    return (
                      <li key={plugin.plugin_id}>
                        <button
                          aria-selected={active}
                          className={`w-full rounded-lg border p-3 text-left transition focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary ${
                            active
                              ? "border-primary/70 bg-primary/10"
                              : "border-border/70 bg-surface-elevated/50 hover:border-primary/50"
                          }`}
                          role="option"
                          type="button"
                          onClick={() => {
                            setSelectedId(plugin.plugin_id);
                            setStatus(null);
                          }}
                        >
                          <p className="font-medium text-text">{plugin.name}</p>
                          <p className="text-xs text-muted">{plugin.plugin_id}</p>
                        </button>
                      </li>
                    );
                  })}
                </ul>
              )}
            </AppCardBody>
          </AppCard>

          <div className="space-y-4 lg:col-span-8">
            <AppCard>
              <AppCardHeader>
                <h2 className="text-lg font-semibold text-text">{t("plugins.detailsTitle")}</h2>
                <p className="text-sm text-muted">{t("plugins.detailsDesc")}</p>
              </AppCardHeader>
              <AppCardBody>
                {!selectedPlugin ? (
                  <p className="rounded-lg border border-dashed border-border p-4 text-sm text-muted">{t("plugins.detailsEmpty")}</p>
                ) : (
                  <>
                    <div className="flex flex-wrap items-start justify-between gap-3 rounded-lg border border-border/70 bg-surface-elevated/50 p-4">
                      <div>
                        <p className="text-lg font-semibold text-text">{selectedPlugin.name}</p>
                        <p className="text-sm text-muted">
                          {selectedPlugin.plugin_id} · v{selectedPlugin.version} · {selectedPlugin.author}
                        </p>
                        <p className="mt-2 text-sm text-text/90">{selectedPlugin.description || t("plugins.descriptionFallback")}</p>
                      </div>
                      <div className="flex gap-2">
                        <Chip radius="sm" variant="flat">
                          {t("plugins.commands", { count: selectedPlugin.commands.length })}
                        </Chip>
                        <Chip radius="sm" variant="flat">
                          {t("plugins.events", { count: selectedPlugin.events.length })}
                        </Chip>
                      </div>
                    </div>

                    <div className="grid gap-3 sm:grid-cols-2">
                      <div className="rounded-lg border border-border/70 bg-surface-elevated/50 p-3">
                        <p className="text-sm font-medium text-text">{t("plugins.listenerCommands")}</p>
                        <ul className="mt-2 space-y-2 text-sm text-muted">
                          {selectedPlugin.commands.length === 0 ? (
                            <li>{t("plugins.none")}</li>
                          ) : (
                            selectedPlugin.commands.slice(0, 6).map((command) => (
                              <li key={command.id}>
                                <p className="font-medium text-text">{command.name}</p>
                                <p className="font-mono text-xs">{command.pattern}</p>
                              </li>
                            ))
                          )}
                        </ul>
                      </div>

                      <div className="rounded-lg border border-border/70 bg-surface-elevated/50 p-3">
                        <p className="text-sm font-medium text-text">{t("plugins.listenerEvents")}</p>
                        <ul className="mt-2 space-y-2 text-sm text-muted">
                          {selectedPlugin.events.length === 0 ? (
                            <li>{t("plugins.none")}</li>
                          ) : (
                            selectedPlugin.events.slice(0, 6).map((event) => (
                              <li key={event.id}>
                                <p className="font-medium text-text">{event.name}</p>
                                <p className="font-mono text-xs">{event.event}</p>
                              </li>
                            ))
                          )}
                        </ul>
                      </div>
                    </div>
                  </>
                )}
              </AppCardBody>
            </AppCard>

            <AppCard>
              <AppCardHeader>
                <h2 className="text-lg font-semibold text-text">{t("plugins.configTitle")}</h2>
                <p className="text-sm text-muted">{t("plugins.configDesc")}</p>
              </AppCardHeader>
              <AppCardBody>
                {!selectedPlugin ? (
                  <p className="text-sm text-muted">{t("plugins.configEmptySelection")}</p>
                ) : !selectedPlugin.config ? (
                  <p className="rounded-lg border border-dashed border-border p-4 text-sm text-muted">{t("plugins.configNoModel")}</p>
                ) : loadingConfig ? (
                  <div className="flex min-h-[140px] items-center justify-center">
                    <Spinner color="primary" label={t("plugins.loadingConfig")} labelColor="primary" />
                  </div>
                ) : (
                  <div className="space-y-4">
                    <GlobalVariableUsageHint t={t} />
                    {canUseSchemaEditor ? (
                      <Tabs
                        aria-label={t("plugins.editorModeAria")}
                        classNames={{
                          cursor: "bg-primary",
                          panel: "pt-3",
                          tab: "data-[hover-unselected=true]:text-primary",
                          tabList: "bg-primary/10 border border-primary/30",
                          tabContent: "group-data-[selected=true]:text-primary-foreground",
                        }}
                        color="primary"
                        selectedKey={editorMode}
                        variant="solid"
                        onSelectionChange={handleModeChange}
                      >
                        <Tab key="schema" title={t("plugins.tabSchema")}>
                          <div className="space-y-3">
                            <SchemaFieldList
                              onChange={handleSchemaFieldChange}
                              pathPrefix={[]}
                              schema={schemaObject as JSONSchema}
                              t={t}
                              value={schemaConfig}
                            />
                          </div>
                        </Tab>
                        <Tab key="json" title={t("plugins.tabJson")}>
                          <div className="space-y-3">
                            <AppTextarea
                              aria-label={t("plugins.configJsonAria")}
                              classNames={{
                                input: "font-mono text-xs leading-6",
                              }}
                              minRows={14}
                              placeholder={t("plugins.jsonPlaceholder")}
                              value={pluginConfigText}
                              onValueChange={(next) => {
                                setPluginConfigText(next);
                                if (editorMode !== "json") {
                                  return;
                                }
                                try {
                                  const parsed = JSON.parse(next) as unknown;
                                  if (isRecord(parsed)) {
                                    setSchemaConfig(parsed);
                                  }
                                } catch {
                                  // Keep textarea content even when JSON is temporarily invalid.
                                }
                              }}
                            />
                            <Divider className="bg-border/70" />
                            <p className="text-xs text-muted">{t("plugins.jsonHint")}</p>
                          </div>
                        </Tab>
                      </Tabs>
                    ) : (
                      <>
                        <AppTextarea
                          aria-label={t("plugins.configJsonAria")}
                          classNames={{
                            input: "font-mono text-xs leading-6",
                          }}
                          minRows={14}
                          placeholder={t("plugins.jsonPlaceholder")}
                          value={pluginConfigText}
                          onValueChange={setPluginConfigText}
                        />
                        <Divider className="bg-border/70" />
                        <p className="text-xs text-muted">{t("plugins.schemaNoVisual")}</p>
                      </>
                    )}
                  </div>
                )}
              </AppCardBody>
              <AppCardFooter>
                <AppButton
                  color="primary"
                  isDisabled={!selectedPlugin || !selectedPlugin.config}
                  isLoading={savingConfig}
                  startContent={<Save className="h-4 w-4" />}
                  onPress={savePluginConfig}
                >
                  {t("plugins.saveConfig")}
                </AppButton>
              </AppCardFooter>
            </AppCard>
          </div>
        </div>
      )}
    </section>
  );
}
