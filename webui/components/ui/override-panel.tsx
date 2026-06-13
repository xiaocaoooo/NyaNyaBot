import React, { useState } from "react";
import { AppButton, AppInput } from "@/components/ui";
import { Divider } from "@heroui/react";
import { Save, Trash2, Plus } from "lucide-react";
import type { PluginListItem, Override } from "@/lib/api/types";

interface OverridePanelProps {
  selectedPlugin: PluginListItem | null;
  target: { type: 'plugin' | 'command'; id?: string };
  saving: boolean;
  onSave: (overrides: Record<string, Override[]>) => Promise<void>;
}

export function OverridePanel({ selectedPlugin, target, saving, onSave }: OverridePanelProps) {
  const [overrides, setOverrides] = useState<Record<string, Override[]>>({});
  const [newRule, setNewRule] = useState({ pattern: "", replacement: "" });
  const [testInput, setTestInput] = useState("");
  const [testResult, setTestResult] = useState("");
  const [matchInfo, setMatchInfo] = useState<{
    commandId: string;
    commandName: string;
    groups: Record<string, string>;
  } | null>(null);
  const [isTesting, setIsTesting] = useState(false);

  React.useEffect(() => {
    if (selectedPlugin) {
      setOverrides(selectedPlugin.state.command_overrides ?? {});
    }
  }, [selectedPlugin?.plugin_id]);

  React.useEffect(() => {
    const runTest = async () => {
      if (!testInput || !selectedPlugin) {
        setTestResult("");
        setMatchInfo(null);
        return;
      }

      setIsTesting(true);
      try {
        const rules = target.id
          ? [...(overrides["global"] ?? []), ...(overrides[target.id] ?? [])]
          : (overrides["global"] ?? []);
        const response = await fetch(`/api/plugins/${selectedPlugin.plugin_id}/test-override`, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            input: testInput,
            overrides: rules,
            commands: selectedPlugin.commands,
          }),
        });
        const data = await response.json();
        setTestResult(data.result);
        setMatchInfo(data.match_info);
      } catch (e) {
        console.error("Override test failed", e);
      } finally {
        setIsTesting(false);
      }
    };

    const timer = setTimeout(runTest, 300);
    return () => clearTimeout(timer);
  }, [testInput, overrides, target.id, selectedPlugin]);

  const handleAddRule = () => {
    if (!newRule.pattern) return;
    
    const ruleId = target.id ?? "global";
    const currentRules = overrides[ruleId] ?? [];
    const updatedRules = [...currentRules, { ...newRule }];
    
    setOverrides({ ...overrides, [ruleId]: updatedRules });
    setNewRule({ pattern: "", replacement: "" });
  };

  const handleRemoveRule = (id: string, index: number) => {
    const ruleId = target.id ?? "global";
    const rules = overrides[ruleId] ?? [];
    const nextRules = rules.filter((_, i) => i !== index);
    const nextOverrides = { ...overrides };
    if (nextRules.length > 0) {
      nextOverrides[ruleId] = nextRules;
    } else {
      delete nextOverrides[ruleId];
    }
    setOverrides(nextOverrides);
  };

  const handleUpdateRule = (id: string, index: number, field: keyof Override, value: string) => {
    const ruleId = target.id ?? "global";
    const rules = [...(overrides[ruleId] ?? [])];
    rules[index] = { ...rules[index], [field]: value };
    setOverrides({ ...overrides, [ruleId]: rules });
  };

  return (
    <div className="space-y-6">
      <div className="space-y-3">
        <p className="text-sm font-medium text-text">
          {target.id ? `指令 ${target.id} 的覆写规则` : "插件全局覆写规则"}
        </p>
        <div className="flex gap-2">
          <AppInput 
            placeholder="匹配正则 (Pattern)" 
            value={newRule.pattern} 
            onValueChange={(v) => setNewRule({...newRule, pattern: v})} 
          />
          <AppInput 
            placeholder="替换内容 (Replacement)" 
            value={newRule.replacement} 
            onValueChange={(v) => setNewRule({...newRule, replacement: v})} 
          />
          <AppButton 
            isIconOnly 
            size="sm" 
            onPress={handleAddRule}
            startContent={<Plus className="h-4 w-4" />}
          >
            添加
          </AppButton>
        </div>
      </div>

      <div className="p-3 rounded-lg bg-surface-200 border border-border/50 space-y-3">
        <p className="text-xs font-medium text-muted">测试替换</p>
        <div className="space-y-2">
          <AppInput 
            placeholder="输入测试文本..." 
            value={testInput} 
            onValueChange={setTestInput} 
          />
          <div className="text-xs p-2 rounded bg-surface-100 border border-border/30 min-h-[2rem] break-all flex items-center gap-2">
            <span className="text-muted mr-2">结果:</span>
            <span className={testResult !== testInput ? "text-primary" : "text-text"}>
              {testResult || <span className="opacity-50">等待输入...</span>}
            </span>
            {isTesting && <span className="text-[10px] animate-pulse text-muted">测试中...</span>}
          </div>
          {matchInfo && (
            <div className="p-2 rounded bg-primary/10 border border-primary/30 text-xs space-y-1">
              <div className="flex items-center gap-2">
                <span className="font-bold text-primary">匹配成功!</span>
                <span className="text-text/70">指令: {matchInfo.commandName} ({matchInfo.commandId})</span>
              </div>
              {Object.keys(matchInfo.groups).length > 0 && (
                <div className="grid grid-cols-2 gap-x-4 gap-y-1 text-muted">
                  {Object.entries(matchInfo.groups).map(([name, value]) => (
                    <React.Fragment key={name}>
                      <span className="font-medium"> {name}:</span>
                      <span className="text-text">{value}</span>
                    </React.Fragment>
                  ))}
                </div>
              )}
            </div>
          )}
        </div>
      </div>

      <Divider />

      <div className="space-y-3">
        {(target.id ? (overrides[target.id] ?? []) : (overrides["global"] ?? [])).map((rule, index) => {
          const ruleId = target.id ?? "global"; 
          return (
            <div key={index} className="flex items-center gap-2 rounded-lg border border-border/50 bg-surface/50 p-2">
              <AppInput 
                className="flex-1"
                value={rule.pattern} 
                onValueChange={(v) => handleUpdateRule(ruleId, index, 'pattern', v)} 
              />
              <AppInput 
                className="flex-1"
                value={rule.replacement} 
                onValueChange={(v) => handleUpdateRule(ruleId, index, 'replacement', v)} 
              />
              <AppButton 
                isIconOnly 
                size="sm" 
                tone="danger" 
                variant="flat"
                onPress={() => handleRemoveRule(ruleId, index)}
              >
                <Trash2 className="h-4 w-4" />
              </AppButton>
            </div>
          );
        })}
        {(target.id ? (overrides[target.id]?.length ?? 0) : (overrides["global"]?.length ?? 0)) === 0 && (
          <p className="text-center text-xs text-muted py-4">暂无覆写规则</p>
        )}
      </div>

      <div className="flex justify-end mt-4">
        <AppButton 
          color="primary" 
          isLoading={saving} 
          onPress={() => onSave(overrides)}
          startContent={<Save className="h-4 w-4" />}
        >
          保存覆写
        </AppButton>
      </div>
    </div>
  );
}
