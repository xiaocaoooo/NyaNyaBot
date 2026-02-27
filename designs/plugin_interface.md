# NyaNyaBot 插件接口设计（v0）

> 目标：让主程序能在运行时加载/卸载插件，并基于 **NapCat / OneBot 11** 的事件上报（event）将事件分发到插件。
>
> 本文聚焦：**插件需要声明的元信息、命令监听、事件监听**，以及 **分发规则（命令匹配后执行对应函数，并传递原始事件）**。

## 1. 背景（来自 apidocs 的事件模型要点）

NapCat/OB11 的上报事件对象（示例 schema：`apidocs/downloads/246111213d0.txt` 中 `OB11Message`）包含通用字段：

- `post_type`：上报类型，枚举值在 schema 中出现（例如 `meta_event` / `request` / `notice` / `message` / `message_sent`）
- 若为消息事件，通常还包含：
  - `message_type`: `private` / `group`
  - `sub_type`: 例如 `friend` / `group` / `normal`
  - `raw_message`: 原始文本（便于命令匹配）
  - `message`: 消息段数组或字符串（取决于 `message_format`）

并且在“快速操作”（`apidocs/downloads/226658889e0.txt`）的 `context` 中还可见：

- `notice_type`、`meta_event_type`（用于通知/元事件进一步分类）

因此：框架内部把“原始上报事件”统一抽象为 **JSON 对象**，插件侧默认接收 **原始 JSON**（不做强类型绑定），并可通过 `post_type` 等字段进行过滤。

---

## 2. 术语

- **Event（事件）**：NapCat/OB11 上报的原始 JSON（原封不动）。
- **Command（命令）**：在 *消息事件* 的 `raw_message` 上应用正则表达式得到的匹配。
- **Content（内容字符串）**：从消息事件派生的纯文本字符串（过滤仅保留 `message` 中 `type == "text"` 的段并拼接），用于命令匹配。
- **Listener（监听器）**：插件声明的“触发条件 + 元信息 + 对应处理函数”。分为：
  - **CommandListener**：命令监听（正则匹配）
  - **EventListener**：事件监听（按字段过滤/匹配）
- **Handler（处理函数）**：插件内实现的函数；当命令/事件命中时由主程序调用。

---

## 3. 插件启动声明（Plugin Descriptor）

插件进程启动并被主程序握手成功后，主程序必须能拿到插件的“自描述信息（Descriptor）”。

### 3.1 必填元信息

插件必须声明：

- 插件名称 `name`
- 插件 ID `plugin_id`
- 版本 `version`
- 作者 `author`
- 描述 `description`

以及：

- 监听的命令 `commands[]`
- 监听的事件 `events[]`

### 3.2 监听器（Listener）必须声明的字段

每个监听器必须声明（命令/事件都一样）：

- 名称 `name`
- ID `id`
- 描述 `description`

并额外声明触发条件：

- 命令监听：`pattern`（正则表达式）
- 事件监听：`event`（事件名字符串，见 §5）

还需要声明“命中后调用哪个处理函数”：

- `handler`：处理函数标识（字符串）

> 约束：
> - `plugin_id` 在全局唯一。
> - 同一插件内 `commands[].id`、`events[].id` 均必须唯一。
> - 全局唯一监听器键：`plugin_id + ":" + listener.id`。

---

## 4. 建议的数据结构（Go 侧表示，设计稿）

> 注意：这是设计文档中的结构草案。实现时可用 `go-plugin` 的 gRPC / net/rpc 承载。

### 4.1 Descriptor

- `PluginDescriptor`
  - `name: string`
  - `plugin_id: string`
  - `version: string`
  - `author: string`
  - `description: string`
  - `commands: []CommandListener`
  - `events: []EventListener`

### 4.2 CommandListener

- `CommandListener`
  - `name: string`
  - `id: string`
  - `description: string`
  - `pattern: string`  
    正则表达式（RE2/Go regexp 兼容）
  - `match_raw?: bool`  
    命令匹配输入来源：`raw_message` 或 `content`（默认 `content`）
  - `handler: string`  
    插件内处理函数标识

#### 4.2.1 content 的构造规则（消息事件）

当 `post_type == "message"` 时，主程序为该事件派生一个子键：`content: string`，用于命令匹配。

- 若事件 `message` 为 **数组（消息段）**：
  - 仅保留 `type == "text"` 的消息段
  - 提取其 `data.text` 并按原顺序拼接
- 若事件 `message` 为 **字符串**：
  - 直接将该字符串作为 `content`
- 若无法解析：
  - `content = ""`

#### 4.2.2 命令匹配输入（raw_message vs content）

- `match_input = "raw_message"`：使用事件 JSON 的 `raw_message`（字段缺失/为空则视为不匹配）
- `match_input = "content"`：使用派生的 `content`（为空则视为不匹配）

默认：`match_input = "raw_message"`。

### 4.3 EventListener

- `EventListener`
  - `name: string`
  - `id: string`
  - `description: string`
  - `event: string`  
    事件名：`"xxx"` 或 `"xxx.xxx"`（见 §5）
  - `handler: string`

> 解释：事件监听改为“字符串事件名”，便于声明与匹配，并与 OneBot 的 `post_type` / `*_type` 结构对齐。

---

## 5. 事件命名/选择器规范

为避免强绑定具体平台细节，同时又能让插件“声明监听什么”，采用 **事件名字符串**。

### 5.1 选择器规则

主程序对每个事件计算一个 `event_key`：

1. 基础键：
   - `event_key = post_type`
2. 若存在对应二级类型，则拼接得到 `event_key_full`：
   - 当 `post_type == "notice"` 且事件包含 `notice_type`：
     - `event_key_full = "notice." + notice_type`
   - 当 `post_type == "meta_event"` 且事件包含 `meta_event_type`：
     - `event_key_full = "meta_event." + meta_event_type`
   - 当 `post_type == "request"` 且事件包含 `request_type`：
     - `event_key_full = "request." + request_type`
   - （可选扩展）当 `post_type == "message"` 且事件包含 `message_type`：
     - `event_key_full = "message." + message_type`

监听器 `event` 的匹配规则：

- 若 `event` 为 `"xxx"`（无点号）：当 `post_type == xxx` 即命中
- 若 `event` 为 `"xxx.yyy"`（有点号）：当 `event_key_full == "xxx.yyy"` 即命中

> 你提出的“检测 `post_type.notice_type` 的拼接匹配”对应：`event = "notice.xxx"`。

### 5.2 例子

- 监听所有消息事件：
  - `event = "message"`
- 监听群消息：
  - `event = "message.group"`（若启用 message_type 拼接）
- 监听私聊消息：
  - `event = "message.private"`（若启用 message_type 拼接）
- 监听元事件：
  - `event = "meta_event"` 或 `event = "meta_event.heartbeat"`
- 监听通知事件：
  - `event = "notice"` 或 `event = "notice.group_upload"`（示例）

---

## 6. 主程序分发规则（核心要求）

### 6.1 输入

主程序收到一个原始上报事件 `rawEventJSON`（字节/字符串均可）。

### 6.2 分发顺序（建议）

1. **事件监听器分发**：
  - 对所有已启用插件的 `events[]` 做 `event` 字符串匹配（§5）
  - 命中则调用对应 `handler`
2. **命令监听器分发**（仅当事件是消息事件时）：
   - 判定 `post_type == "message"`（或未来支持 `message_sent` 视需求）
  - 为事件构造 `content`（§4.2.1）
  - 根据 `commands[].match_input` 选择 `raw_message` 或 `content` 作为匹配输入
  - 对每个 `commands[].pattern` 执行正则匹配
   - 命中则调用对应 `handler`

> 若你希望“命令优先于事件”也可调整顺序；本设计文档默认 **事件监听更通用**，命令监听更偏业务。

### 6.3 命令命中后的调用要求（按你的需求）

- **如果匹配命令则执行插件的对应函数，并传递原始收到的事件**。

因此，调用插件处理函数的最小入参必须包含：

- `event_raw_json`：原始事件 JSON（不改写、不裁剪）

（可选增强，不影响“最小要求”）

- `command_match`：正则匹配结果（例如整体匹配、捕获组），便于插件使用

---

## 7. RPC/ABI 形态（建议）

考虑到 go-plugin 跨进程通信，建议插件对外暴露 3 个核心 RPC：

1. `Describe() -> PluginDescriptor`
2. `Handle(listener_id, event_raw_json, match?) -> HandleResult`
3. `Shutdown()`

并建议支持一种“主程序代调用 OneBot”的方式（二选一即可，或两者都支持）：

- **方式 A（推荐，声明式）**：插件在 `HandleResult.actions[]` 中返回 `action+params`，由主程序执行（见 §7.1）
- **方式 B（命令式）**：插件可调用主程序提供的 `CallOneBot(action, params)` RPC

其中：

- `listener_id` 为插件声明的 `commands[].id` 或 `events[].id`
- 主程序负责做“选择器匹配/正则匹配”，插件负责处理业务

> 这样可以保证：
> - 主程序可统一调度与限流
> - 插件接口稳定（新增事件字段不破坏插件）

### 7.1 CallOneBot 方式（命令式）

支持：插件传递 `action` + `params`，由主程序直接调用 OneBot 方法。

因此把返回结构明确为：

- `CallOneBot(action: string, params: object) -> CallResult`

---

## 8. 插件声明示例（JSON，仅示意）

```json
{
  "name": "Echo",
  "plugin_id": "com.example.echo",
  "version": "1.0.0",
  "author": "you",
  "description": "回声测试插件",
  "commands": [
    {
      "name": "echo",
      "id": "cmd.echo",
      "description": "匹配 /echo xxx 并回声",
      "pattern": "^/echo\\s+(.+)$",
      "match_raw": false,
      "handler": "HandleEcho"
    }
  ],
  "events": [
    {
      "name": "all messages",
      "id": "evt.all_messages",
      "description": "观测所有 message 上报",
      "event": "message",
      "handler": "HandleAnyMessage"
    }
  ]
}
```

---

## 9. 兼容性与约束

- **事件结构向前兼容**：插件以 `event_raw_json` 处理，新增字段不破坏旧插件。
- **正则引擎约束**：命令 `pattern` 使用 Go `regexp`（RE2）语法；不支持回溯特性。
- **安全与资源**：主程序应对 `Handle` 设置超时与并发上限（避免插件卡死拖垮主进程）。

---

## 10. 下一步（可拆分到后续设计）

- 完善 OneBot 调用结果回传（`actions[]` 执行结果 / 错误处理）
- 插件配置（schema、默认值、Web UI 表单生成）
- 权限系统（哪些命令/事件允许插件处理）
- 事件/命令优先级与“是否阻断后续监听器”策略
