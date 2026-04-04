# AGENTS.md — NyaNyaBot

## 项目概述

NyaNyaBot 是一个基于 OneBot 11 协议的可扩展 QQ 机器人宿主框架，通过反向 WebSocket 对接 NapCat，提供进程隔离的插件系统、事件分发、Web 管理界面和配置模板等功能。

本项目是一个**宿主框架**，负责加载和运行插件。它本身不提供具体的机器人功能，而是为插件提供运行环境、事件路由、OneBot API 桥接和跨插件调用机制。

## 技术栈速查

| 领域 | 技术 | 版本 |
|------|------|------|
| 后端语言 | Go | 1.25.6 |
| 插件框架 | hashicorp/go-plugin (net/rpc) | v1.7.0 |
| WebSocket | nhooyr.io/websocket | v1.8.17 |
| 日志 | hashicorp/go-hclog | v1.6.3 |
| 前端框架 | Next.js + React 18 + TypeScript | Next.js 14 |
| UI 库 | HeroUI + Tailwind CSS | — |
| 包管理 (前端) | pnpm | — |
| 通信协议 | OneBot 11 | — |

## 目录结构

```
NyaNyaBot/
├── cmd/                                    # 可执行程序入口
│   ├── nyanyabot/                          # 主程序入口 (main.go)
│   ├── nyanyabot-plugin-echo/              # Echo 测试插件（演示依赖调用）
│   ├── nyanyabot-plugin-configdump/        # 配置热更新测试插件
│   ├── nyanyabot-plugin-builtin-status/    # 状态查询内置插件
│   ├── nyanyabot-plugin-blobserver/        # Blob 服务插件
│   └── nyanyabot-plugin-screenshot/        # 截图插件
├── internal/                               # 核心内部包（不可外部导入）
│   ├── app/                                # 应用组装层
│   ├── config/                             # 配置管理（持久化到 data/config.json）
│   ├── configtmpl/                         # 配置模板引擎
│   ├── dispatch/                           # 事件分发器
│   ├── onebot/
│   │   ├── ob11/                           # OneBot 11 类型定义（Event/APIRequest/APIResponse）
│   │   └── reversews/                      # 反向 WebSocket 服务器
│   ├── plugin/                             # 插件接口、Manager、错误类型
│   │   └── transport/                      # go-plugin RPC 传输层（Plugin/Host 双向 RPC）
│   ├── pluginhost/                         # 插件宿主（进程加载、依赖拓扑排序）
│   ├── stats/                              # 运行时统计（收发消息计数、运行时间）
│   ├── util/                               # 工具函数（文件系统操作）
│   └── web/                                # Web 服务器 + go:embed 嵌入前端
├── webui/                                  # Next.js 前端源码
├── plugins/                                # 编译后的插件二进制存放目录（自动扫描）
├── data/                                   # 运行时数据（config.json）
├── designs/                                # 设计文档
│   ├── tech_stack.md                       # 技术架构设计
│   └── plugin_interface.md                 # 插件接口规范
├── scripts/                                # 构建脚本
│   └── sync_webui_export.sh               # 将 Next.js 静态导出同步到 Go embed 目录
├── go.mod / go.sum                         # Go 依赖
└── AGENTS.md                               # 本文件
```

## 核心模块详解

### 1. internal/app/ — 应用组装层

`App` 结构体聚合所有核心组件，是整个程序的控制中心：

```go
type App struct {
    Logger *slog.Logger
    Store  *config.Store       // 配置存储
    PM     *plugin.Manager     // 插件管理器
    PH     *pluginhost.Host    // 插件宿主（进程管理）
    Disp   *dispatch.Dispatcher // 事件分发器
    OB     *reversews.Server   // OneBot 反向 WebSocket
    Web    *http.Server        // Web 管理界面
    Stats  *stats.Stats        // 运行时统计
}
```

`app.New()` 负责组装所有组件并建立连接：OB 事件 → Dispatcher 路由 → 插件处理。

### 2. internal/config/ — 配置管理

- 配置文件路径：`data/config.json`
- 使用 `Store` 结构体管理，支持原子写入（先写 `.tmp` 再 `os.Rename`）
- `AppConfig` 结构：`OneBot` / `WebUI` / `Globals` / `Plugins`
- `Store.Update(fn)` 接受回调函数修改配置，自动保存并下发
- WebUI 密码在首次创建时自动生成（24 位 base64 随机字符串）

### 3. internal/configtmpl/ — 配置模板引擎

在插件配置下发前，对 JSON 字符串值执行变量替换：

| 语法 | 含义 | 示例 |
|------|------|------|
| `${global:name}` | 引用 globals 中的变量 | `${global:api_key}` |
| `${env:NAME}` | 引用系统环境变量 | `${env:HOME}` |
| `\${global:name}` | 转义，保留字面量 | 输出 `${global:name}` |
| 未知变量 | 保留原样 | `${global:missing}` → 不替换 |

模板替换仅作用于 JSON 字符串值，不影响数字、布尔值等。

### 4. internal/dispatch/ — 事件分发器

事件处理流程：

1. **事件监听器匹配**：根据 `post_type` 和二级类型（`message_type`/`notice_type`/`request_type`/`meta_event_type`）匹配 `EventListener.Event` 字段
   - 匹配 `message` → 匹配所有消息类型事件
   - 匹配 `message.group` → 仅匹配群消息
2. **命令监听器匹配**（仅 `post_type == "message"` 时）：对消息文本执行正则匹配
   - 默认对 `message` 字段提取纯文本内容
   - 若 `CommandListener.MatchRaw == true`，则对 `raw_message` 匹配
   - 正则使用 Go RE2 语法（**不支持回溯断言**，如 `(?=...)`、`(?!...)`）
3. 匹配成功后调用插件的 `Handle(ctx, listenerID, event, match)`

### 5. internal/onebot/reversews/ — 反向 WebSocket 服务器

- NapCat 作为客户端主动连接本程序
- 支持 OneBot 11 标准的事件接收和 API 调用
- `Server.Call(ctx, action, params)` 发送 API 请求到 NapCat

### 6. internal/plugin/ — 插件接口与管理

**Plugin 接口**（5 个方法）：

```go
type Plugin interface {
    Descriptor(ctx) (Descriptor, error)          // 返回插件元信息
    Configure(ctx, json.RawMessage) error         // 接收配置推送
    Invoke(ctx, method, params, callerID) (json.RawMessage, error)  // 跨插件调用
    Handle(ctx, listenerID, event, *CommandMatch) (HandleResult, error)  // 事件处理
    Shutdown(ctx) error                           // 关闭清理
}
```

**Manager** 职责：
- `Register` / `RegisterWithDescriptor`：注册插件
- `Get(pluginID)`：获取插件实例和描述
- `Entries()`：获取所有插件的描述快照（供分发器使用）
- `CallDependency(ctx, callerID, targetID, method, params)`：执行跨插件调用，验证依赖关系和导出方法

**StructuredError**：结构化错误类型，包含 `Code`（FORBIDDEN / NOT_FOUND / INVALID_PARAMS / INTERNAL）和 `Message`。

### 7. internal/plugin/transport/ — RPC 传输层

使用 hashicorp/go-plugin 的 net/rpc 协议，定义宿主与插件进程之间的双向 RPC：

**宿主调插件**（PluginRPC）：
- `Describe` → 获取 Descriptor
- `Configure` → 推送配置
- `Invoke` → 跨插件方法调用
- `Handle` → 事件处理
- `Shutdown` → 关闭

**插件调宿主**（HostRPC）：
- `CallOneBot(action, params)` → 调用 OneBot API（如发送消息）
- `CallDependency(targetPluginID, method, params)` → 调用依赖插件的导出方法
- `GetStats()` → 获取运行时统计

握手配置：
- `MagicCookieKey: "NYANYABOT_PLUGIN"`, `MagicCookieValue: "1"`
- `ProtocolVersion: 1`

### 8. internal/pluginhost/ — 插件宿主

**三阶段加载流程**（`LoadDir`）：

1. **Phase 1 — 启动进程**：扫描 `plugins/` 目录，启动所有 `nyanyabot-plugin-*` 可执行文件
2. **Phase 2 — 读取 Descriptor**：对每个插件进程调用 `Describe()`，验证接口兼容性（`probeInvokeCompatibility`），校验 Descriptor 合法性，检测重复 PluginID
3. **Phase 3 & 4 — 拓扑排序 + 注册 + 配置下发**：
   - 使用 Kahn 算法按依赖关系拓扑排序
   - 循环依赖和缺失依赖的插件会被拒绝
   - 按顺序注册插件、绑定 HostAPI、推送配置

### 9. internal/web/ — Web 管理界面

- REST API 支持：auth（Session 认证）、config、globals、plugins 管理
- 前端通过 `go:embed` 嵌入 Next.js 静态导出
- 认证方式：密码 → Session cookie

### 10. OneBot 11 类型定义 (internal/onebot/ob11/)

```go
type Event = json.RawMessage  // 未类型化的原始事件，保持向前兼容

type APIRequest struct {
    Action string      `json:"action"`
    Params interface{} `json:"params,omitempty"`
    Echo   string      `json:"echo,omitempty"`
}

type APIResponse struct {
    Status  string          `json:"status"`
    RetCode int             `json:"retcode"`
    Data    json.RawMessage `json:"data,omitempty"`
}
```

## 插件接口规范

### Plugin 接口方法

| 方法 | 调用时机 | 说明 |
|------|---------|------|
| `Descriptor` | 插件加载时 | 返回插件元信息（ID、名称、版本、命令、事件等） |
| `Configure` | 加载时 + 配置修改时 | 接收 JSON 配置，必须幂等 |
| `Invoke` | 被其他插件调用时 | 仅能调用 `Exports` 中声明的方法 |
| `Handle` | 事件匹配时 | 根据 listenerID 分发到具体处理逻辑 |
| `Shutdown` | 宿主关闭时 | 清理资源 |

### Descriptor 结构

```go
type Descriptor struct {
    Name         string           // 显示名称
    PluginID     string           // 唯一标识，格式：external.xxx 或 builtin.xxx
    Version      string           // 语义化版本
    Author       string           // 作者
    Description  string           // 描述
    Dependencies []string         // 直接依赖的 PluginID 列表
    Exports      []ExportSpec     // 暴露给其他插件的方法
    Config       *ConfigSpec      // 配置 schema 和默认值（可选）
    Commands     []CommandListener // 命令监听器
    Events       []EventListener  // 事件监听器
}
```

### CommandListener

```go
type CommandListener struct {
    Name     string // 显示名称
    ID       string // 唯一 ID（如 "cmd.echo"）
    Pattern  string // 正则模式（RE2 语法）
    MatchRaw bool   // true=匹配 raw_message，false=匹配 message 纯文本
    Handler  string // 处理方法名（传给 Handle 的 listenerID）
}
```

### EventListener

```go
type EventListener struct {
    Name    string // 显示名称
    ID      string // 唯一 ID
    Event   string // 事件类型："message"、"message.group"、"notice"、"notice.group_increase" 等
    Handler string // 处理方法名
}
```

### 宿主 API（插件侧可用）

插件通过 `transport.Host()` 获取 `HostRPCClient`，可用以下方法：

| 方法 | 说明 |
|------|------|
| `CallOneBot(ctx, action, params)` | 调用 OneBot API（如 `send_group_msg`、`send_private_msg`） |
| `CallDependency(ctx, targetPluginID, method, params)` | 调用依赖插件的导出方法 |
| `GetStats(ctx)` | 获取运行时统计（收发计数、启动时间、运行时长） |

### 插件进程 main.go 模板

```go
package main

import (
    hclog "github.com/hashicorp/go-hclog"
    "github.com/hashicorp/go-plugin"
    "github.com/xiaocaoooo/nyanyabot/internal/plugin/transport"
)

func main() {
    logger := hclog.New(&hclog.LoggerOptions{
        Name:  "nyanyabot-plugin-xxx",
        Level: hclog.Info,
    })

    plugin.Serve(&plugin.ServeConfig{
        HandshakeConfig: transport.Handshake(),
        Plugins: plugin.PluginSet{
            transport.PluginName: &transport.Map{PluginImpl: &MyPlugin{}},
        },
        Logger: logger,
    })
}
```

## 构建、运行、测试命令

### 构建主程序

```bash
cd cmd/nyanyabot && go build -o ../../bin/nyanyabot .
```

### 运行主程序

```bash
cd cmd/nyanyabot && ../../bin/nyanyabot
# 或直接从项目根目录
./bin/nyanyabot
```

程序启动后：
- WebUI 监听 `0.0.0.0:3000`（含自动登录 URL 输出到日志）
- OneBot 反向 WebSocket 监听 `0.0.0.0:3001`

### 编译插件

```bash
# 单个插件
cd cmd/nyanyabot-plugin-echo && go build -o ../../plugins/nyanyabot-plugin-echo .

# 所有插件（示例）
for dir in cmd/nyanyabot-plugin-*; do
    name=$(basename "$dir")
    cd "$dir" && go build -o "../../plugins/$name" . && cd ../..
done
```

### 构建前端

```bash
cd webui && pnpm install && pnpm run build
```

或使用同步脚本（构建后自动同步到 Go embed 目录）：

```bash
bash scripts/sync_webui_export.sh
```

### 运行测试

```bash
# 全部测试
go test ./...

# 特定包
go test ./internal/config/
go test ./internal/configtmpl/
go test ./internal/plugin/
go test ./internal/plugin/transport/
go test ./internal/pluginhost/
go test ./internal/web/
```

### Go 工具链

```bash
go vet ./...           # 静态分析
go mod tidy            # 清理依赖
```

## 配置说明

### 配置文件位置

`data/config.json`（相对于工作目录）

### 配置结构

```json
{
  "onebot": {
    "reverse_ws": {
      "listen_addr": "0.0.0.0:3001"
    }
  },
  "webui": {
    "listen_addr": "0.0.0.0:3000",
    "password": "<自动生成，24位base64>"
  },
  "globals": {
    "api_key": "sk-xxx",
    "base_url": "https://api.example.com"
  },
  "plugins": {
    "external.echo": {
      "prefix": "Echo: "
    },
    "external.screenshot": {
      "url": "${global:base_url}/render",
      "token": "${global:api_key}"
    }
  }
}
```

### 配置热更新

通过 WebUI 修改配置后，主程序自动调用各插件的 `Configure` 方法下发新配置，无需重启。

配置模板变量在下发时自动替换（详见 `internal/configtmpl/`）。

## 开发约定

### 代码规范

1. **Go 版本**：必须使用 Go 1.25.6 或以上
2. **内部包**：`internal/` 下的包不可被外部项目直接导入
3. **错误处理**：插件间通信使用 `StructuredError`，包含 `Code` 和 `Message`
4. **日志**：主程序使用 `slog`，插件使用 `hclog`

### 插件开发约定

1. **可执行文件命名**：必须以 `nyanyabot-plugin-` 为前缀（如 `nyanyabot-plugin-echo`）
2. **Plugin ID 命名**：
   - 第三方插件：`external.xxx`（如 `external.echo`）
   - 内置插件：`builtin.xxx`（如 `builtin.status`）
3. **正则语法**：使用 Go RE2，不支持回溯断言（`(?=...)`、`(?!...)`）
4. **进程隔离**：插件崩溃不影响主程序，反之亦然
5. **配置幂等**：`Configure` 方法必须幂等，可能被多次调用
6. **依赖声明**：在 `Descriptor.Dependencies` 中声明直接依赖，宿主自动处理拓扑排序

### 前端开发约定

1. 使用 pnpm 作为包管理器
2. 构建后需通过 `scripts/sync_webui_export.sh` 同步到 Go embed 目录
3. Next.js 使用静态导出模式（`next export`）

### 文件放置

- 插件源码 → `cmd/nyanyabot-plugin-xxx/`
- 编译后插件 → `plugins/nyanyabot-plugin-xxx`
- 运行时数据 → `data/`
- 设计文档 → `designs/`

## 常见任务指引

### 如果你要添加一个新插件

1. 在 `cmd/` 下创建 `nyanyabot-plugin-<name>/main.go`
2. 实现 `Plugin` 接口的 5 个方法：`Descriptor`、`Configure`、`Invoke`、`Handle`、`Shutdown`
3. 在 `Descriptor` 中声明 PluginID（`external.xxx` 或 `builtin.xxx`）、Commands、Events、Exports、Dependencies
4. 在 `main()` 中使用 `plugin.Serve` 注册插件（参考 `cmd/nyanyabot-plugin-echo/main.go`）
5. 编译到 `plugins/` 目录：`cd cmd/nyanyabot-plugin-<name> && go build -o ../../plugins/nyanyabot-plugin-<name> .`
6. 重启主程序即可自动加载

### 如果你要修改事件分发逻辑

编辑 `internal/dispatch/dispatcher.go`。关键函数：
- `Dispatch()` — 主入口
- `computeEventKeys()` — 计算事件匹配键（如 `message.group`）
- `matchEvent()` — 判断事件是否匹配
- `deriveContent()` — 从消息段中提取纯文本

### 如果你要添加新的 OneBot API 支持

API 调用通过 `reversews.Server.Call(ctx, action, params)` 发送到 NapCat，不需要额外注册。只需在插件中调用 `host.CallOneBot(ctx, "action_name", params)` 即可。

### 如果你要修改配置结构

1. 修改 `internal/config/config.go` 中的 `AppConfig` 及相关结构体
2. 在 `ensureDefaultsLocked` 中为新字段设置默认值
3. 如需模板支持，确保新字段为字符串类型

### 如果你要修改 Web API

编辑 `internal/web/server.go`。当前 API 路径：
- `POST /api/auth/login` — 登录
- `GET /api/config` — 获取配置
- `PUT /api/config` — 更新配置
- `GET /api/globals` — 获取全局变量
- `PUT /api/globals` — 更新全局变量
- `GET /api/plugins` — 获取插件列表

### 如果你要修改插件接口

1. 修改 `internal/plugin/api.go` 中的 `Plugin` 接口和相关类型
2. 同步更新 `internal/plugin/transport/proto.go` 中的 RPC 参数/返回值结构
3. 同步更新 `internal/plugin/transport/plugin_rpc.go` 中的 Server/Client 实现
4. 更新所有已有的插件实现

### 如果前端构建后页面未更新

```bash
cd webui && pnpm run build && bash ../scripts/sync_webui_export.sh
```

确保 `webui/out/` 的静态文件已同步到 `internal/web/` 的 embed 目录。

## 项目间关系

```
                    ┌─────────────────┐
                    │   NapCat (QQ)   │
                    └────────┬────────┘
                             │ WebSocket
                    ┌────────▼────────┐
                    │   NyaNyaBot     │  ← 本项目：宿主框架
                    │  (宿主框架)      │
                    └────────┬────────┘
                             │ go-plugin (net/rpc)
              ┌──────────────┼──────────────┐
              │              │              │
     ┌────────▼───┐  ┌──────▼──────┐  ┌────▼────────┐
     │ amiabot-   │  │ amiabot-    │  │ 其他插件     │
     │ plugin-sdk │  │ pages       │  │             │
     └────────────┘  └─────────────┘  └─────────────┘
```

- **NyaNyaBot**（本项目）：宿主框架，负责插件加载、事件分发、配置管理
- **amiabot-plugin-sdk**：插件 SDK，定义插件与宿主通信的标准化接口
- **AmiaBot**：提供 7 个外置插件供本框架加载
- **amiabot-pages**：页面渲染服务，被部分插件调用（如截图插件）
- **NapCat**：OneBot 11 实现，连接 QQ 协议，通过反向 WebSocket 与本程序通信
