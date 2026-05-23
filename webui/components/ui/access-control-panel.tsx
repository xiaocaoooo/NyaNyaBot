"use client";

import { Tabs, Tab, Button, Avatar } from "@heroui/react";
import { ShieldCheck, ShieldAlert, Type, List, Plus, Trash2 } from "lucide-react";
import { useState, useMemo } from "react";
import { AppTextarea } from "./textarea";
import { AppInput } from "./input";
import { cn } from "@/lib/utils/cn";
import { useI18n } from "@/components/providers/i18n-provider";
import { useInfoCache } from "@/lib/hooks/use-info-cache";

interface AccessControlSectionProps {
  title: string;
  description: string;
  value: string;
  onValueChange: (value: string) => void;
  type: "whitelist" | "blacklist";
  isUser: boolean;
}

function AccessControlSection({ title, description, value, onValueChange, type, isUser }: AccessControlSectionProps) {
  const { t } = useI18n();
  const { getName } = useInfoCache();
  const [mode, setMode] = useState<"text" | "list">("list");
  const [newId, setNewId] = useState("");

  const ids = useMemo(() => {
    return value
      .split(/[\n,]/)
      .map((s) => s.trim())
      .filter((s) => s !== "");
  }, [value]);

  const removeId = (idToRemove: string) => {
    const nextIds = ids.filter((id) => id !== idToRemove);
    onValueChange(nextIds.join("\n"));
  };

  const addId = () => {
    const trimmed = newId.trim();
    if (!trimmed) return;
    
    const addedIds = trimmed.split(/[,，\s]+/).map(s => s.trim()).filter(s => s !== "");
    const uniqueNewIds = addedIds.filter(id => !ids.includes(id));
    
    if (uniqueNewIds.length > 0) {
      const nextIds = [...ids, ...uniqueNewIds];
      onValueChange(nextIds.join("\n"));
    }
    setNewId("");
  };

  const isWhitelist = type === "whitelist";

  return (
    <div className={cn(
      "flex flex-col gap-3 rounded-xl border p-4 transition-colors",
      isWhitelist 
        ? "border-success/20 bg-success/5" 
        : "border-danger/20 bg-danger/5"
    )}>
      <div className="flex items-start justify-between gap-4">
        <div className="flex items-center gap-2">
          {isWhitelist ? (
            <ShieldCheck className="h-5 w-5 text-success" />
          ) : (
            <ShieldAlert className="h-5 w-5 text-danger" />
          )}
          <div>
            <h3 className="text-sm font-bold text-text">{title}</h3>
            <p className="text-[11px] text-muted leading-tight">{description}</p>
          </div>
        </div>
        <Tabs 
          size="sm" 
          radius="full" 
          selectedKey={mode} 
          onSelectionChange={(k) => setMode(k as any)}
          classNames={{
            tabList: "bg-surface/50 p-1",
            cursor: isWhitelist ? "bg-success text-success-foreground" : "bg-danger text-danger-foreground",
          }}
        >
          <Tab key="text" title={<Type className="h-3 w-3" />} />
          <Tab key="list" title={<List className="h-3 w-3" />} />
        </Tabs>
      </div>

      {mode === "text" ? (
        <AppTextarea
          placeholder={t("config.accessControlPlaceholder")}
          value={value}
          onValueChange={onValueChange}
          minRows={5}
          maxRows={10}
          classNames={{
            input: "text-xs font-mono",
            inputWrapper: "bg-surface/80"
          }}
        />
      ) : (
        <div className="space-y-3">
          <div className="h-[160px] overflow-y-auto rounded-lg border border-border/40 bg-surface/60 p-2 space-y-1">
            {ids.length === 0 ? (
              <p className="text-xs text-muted/60 italic p-4 text-center">
                {t("plugins.none")}
              </p>
            ) : (
                ids.map((idStr) => {
                const id = parseInt(idStr, 10);
                const name = getName(id, isUser ? 'user' : 'group');
                const avatarUrl = isUser 
                  ? `https://q1.qlogo.cn/g?b=qq&nk=${id}&s=640`

                  : `https://p.qlogo.cn/gh/${id}/${id}/640`;
                
                return (
                  <div key={idStr} className="flex items-center justify-between rounded-md bg-surface/50 p-1.5 hover:bg-surface-elevated/50 transition">
                    <div className="flex items-center gap-2 overflow-hidden">
                      <Avatar src={avatarUrl} size="sm" className="h-6 w-6" />
                      <div className="flex flex-col truncate">
                        <span className="text-xs font-medium text-text truncate">{name}</span>
                        <span className="text-[10px] text-muted font-mono">{id}</span>
                      </div>
                    </div>
                    <Button 
                      isIconOnly 
                      size="sm" 
                      variant="light" 
                      className="h-6 w-6 text-muted hover:text-danger"
                      onPress={() => removeId(idStr)}
                    >
                      <Trash2 className="h-3 w-3" />
                    </Button>
                  </div>
                );
              })
            )}
          </div>
          <div className="flex gap-2">
            <AppInput
              size="sm"
              placeholder={t("config.addIdPlaceholder")}
              value={newId}
              onValueChange={setNewId}
              onKeyDown={(e) => e.key === "Enter" && addId()}
              classNames={{
                input: "text-xs font-mono",
                inputWrapper: "bg-surface/80 h-8"
              }}
            />
            <Button 
              isIconOnly 
              size="sm" 
              color={isWhitelist ? "success" : "danger"} 
              variant="flat"
              onPress={addId}
              className="h-8 w-8 min-w-0"
            >
              <Plus className="h-4 w-4" />
            </Button>
          </div>
        </div>
      )}
    </div>
  );
}

export interface AccessControlPanelProps {
  whitelistUsers: string;
  setWhitelistUsers: (v: string) => void;
  blacklistUsers: string;
  setBlacklistUsers: (v: string) => void;
  whitelistGroups: string;
  setWhitelistGroups: (v: string) => void;
  blacklistGroups: string;
  setBlacklistGroups: (v: string) => void;
}

export function AccessControlPanel({
  whitelistUsers, setWhitelistUsers,
  blacklistUsers, setBlacklistUsers,
  whitelistGroups, setWhitelistGroups,
  blacklistGroups, setBlacklistGroups
}: AccessControlPanelProps) {
  const { t } = useI18n();

  return (
    <Tabs 
      fullWidth 
      aria-label="Access Control Tabs"
      classNames={{
        tabList: "bg-surface-elevated/50",
        panel: "pt-4"
      }}
    >
      <Tab key="user" title={t("config.accessControlUserTab")}>
        <div className="grid gap-4 sm:grid-cols-2">
          <AccessControlSection
            type="whitelist"
            isUser={true}
            title={t("config.whitelistUsersTitle")}
            description={t("config.accessControlUserDesc")}
            value={whitelistUsers}
            onValueChange={setWhitelistUsers}
          />
          <AccessControlSection
            type="blacklist"
            isUser={true}
            title={t("config.blacklistUsersTitle")}
            description={t("config.accessControlUserDesc")}
            value={blacklistUsers}
            onValueChange={setBlacklistUsers}
          />
        </div>
      </Tab>
      <Tab key="group" title={t("config.accessControlGroupTab")}>
        <div className="grid gap-4 sm:grid-cols-2">
          <AccessControlSection
            type="whitelist"
            isUser={false}
            title={t("config.whitelistGroupsTitle")}
            description={t("config.accessControlGroupDesc")}
            value={whitelistGroups}
            onValueChange={setWhitelistGroups}
          />
          <AccessControlSection
            type="blacklist"
            isUser={false}
            title={t("config.blacklistGroupsTitle")}
            description={t("config.accessControlGroupDesc")}
            value={blacklistGroups}
            onValueChange={setBlacklistGroups}
          />
        </div>
      </Tab>
    </Tabs>
  );
}
