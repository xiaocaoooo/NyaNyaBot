# NyaNyaBot

用 Rust（edition 2024）重写的可扩展机器人**宿主**。

NyaNyaBot 通过**本机 gRPC**（[`nyanyabot-proto`](https://github.com/xiaocaoooo/nyanyabot-proto) 中的 `nyanyabot.plugin.v1`）加载独立进程插件，提供 **OneBot 11** 反向 WebSocket、**Next.js** WebUI，并保持既有配置 JSON / REST 响应形态。

> 旧 Go / HashiCorp go-plugin 插件二进制**不兼容**，不会被加载。

## 功能概览

- 异步运行时：`tokio` + `axum`（Web API / 静态前端）
- OneBot 11 反向 WebSocket（`tokio-tungstenite`）
- 插件管理：发现 `nyanyabot-plugin-*`、Descriptor 校验、依赖拓扑、配置热更新、崩溃监控与自动重启、空闲休眠、优雅关闭
- 共享本机 `HostService` + 每插件独立 token（`x-nyanyabot-token`）
- 可选 PostgreSQL 聊天记录 / 触发记录（`sqlx`）
- 内置插件：`builtin.status`、`external.echo`
- WebUI：`webui/`（Next.js + TypeScript），经 `rust-embed` 嵌入

## 目录结构

```text
NyaNyaBot/
  crates/nyanyabot/                 # 宿主库 + 主程序
  crates/nyanyabot-plugin-builtin-status/
  crates/nyanyabot-plugin-echo/
  crates/xtask/                     # cargo xtask stage
  webui/                            # Next.js 前端
  config.example.json
  Dockerfile / docker-compose.yml
  designs/                          # 设计文档
```

协议路径依赖：`../nyanyabot-proto`。

## 环境要求

- 支持 **edition 2024** 的 Rust 工具链（Docker/CI 使用 1.97 量级镜像）
- 可选：Node 22 + `pnpm` 构建前端
- 可选：PostgreSQL（chat/trigger log）
- 目标平台：Linux / Windows / macOS 原生（仅本机插件进程）

## 快速开始

```bash
# 1) 编译并 stage 主程序与示例插件
cargo xtask stage

# 2) 准备 data 与配置
mkdir -p data plugins
cp config.example.json data/config.json   # 按需修改

# 3) 在 NyaNyaBot 仓库根目录运行
./nyanyabot
```

默认端口（可在 `data/config.json` 覆盖）：

| 端口 | 服务 |
|------|------|
| `3000` | WebUI + REST API |
| `3001` | OneBot 11 反向 WebSocket |

浏览器打开 `http://127.0.0.1:3000/`（密码见配置中的 WebUI password）。

### 配置要点

参考 `config.example.json`：

- `onebot.reverse_ws.listen_addr`
- `webui.listen_addr` / `webui.password`
- `message_prefix` — 正则，推荐命名捕获组 `content`
- `plugins.<plugin_id>` — 各插件 JSON 配置
- `chat_log.database_uri` / `trigger_log.*` — 可选 PostgreSQL

兼容约定：现有 `data/config.json` 键与 REST 结构保持；已删除插件的遗留键保留但忽略。

## 插件

### 本仓库内置

| 二进制 | 插件 ID | 作用 |
|--------|---------|------|
| `nyanyabot-plugin-builtin-status` | `builtin.status` | 状态命令 |
| `nyanyabot-plugin-echo` | `external.echo` | 回声测试（`cmd.echo`） |

`cargo xtask stage` 会复制到 `plugins/`。Windows 下带 `.exe` 后缀。

### 外部插件（AmiaBot）

在 [AmiaBot](https://github.com/xiaocaoooo/AmiaBot) 中 `cargo xtask stage`，将其 `plugins/` 与宿主共用或挂载到宿主加载目录。

发现规则：

- Linux/macOS：可执行文件名前缀 `nyanyabot-plugin-`
- Windows：同前缀 + `.exe`

启动握手超时：**10 秒**。

### 握手摘要

1. 插件监听 `127.0.0.1:0`
2. stdout 输出一行 readiness JSON
3. 日志走 stderr
4. 宿主用插件 token 鉴权后 `Describe` / `AttachHost` / `Configure`

完整协议见 [nyanyabot-proto](https://github.com/xiaocaoooo/nyanyabot-proto)。

## WebUI

```bash
cd webui
pnpm install
pnpm build          # 生成 webui/out
```

编译时由 `crates/nyanyabot/build.rs` 将 `webui/out` 同步到 gitignore 的 `generated/frontend` 并 `rust-embed`。若缺少 `webui/out`，则嵌入 `frontend-placeholder` 以便测试编译。生产请先 `pnpm build` 或 `cargo xtask frontend`；Docker 同样把 `webui/out` 提供给 build.rs。

## Docker

**从 monorepo 父目录**构建（需要同时有 `nyanyabot-proto` 与 `NyaNyaBot`）：

```bash
# 建议先构建前端静态导出
cd NyaNyaBot/webui && pnpm build && cd ../..

docker build -f NyaNyaBot/Dockerfile -t nyanyabot .
```

或：

```bash
cd NyaNyaBot
docker compose build && docker compose up -d
```

运行示例：

```bash
docker run --rm -p 3000:3000 -p 3001:3001 \
  -v "$PWD/data:/app/data" \
  -v "$PWD/plugins:/app/plugins" \
  nyanyabot
```

注意：

- 镜像以 uid/gid **10001** 运行，挂载的 `data/` 需可写
- 更多说明见 `DOCKER.md`

## 开发与验收

```bash
cargo fmt --check
cargo clippy --workspace --all-targets -- -D warnings
cargo test --workspace
cargo xtask stage
```

集成测试见 `crates/nyanyabot/tests/`：

- gRPC 子进程握手 / 鉴权 / 崩溃重启 / 空闲休眠
- 假 OneBot 客户端的反向 WebSocket E2E
- PostgreSQL chat/trigger log 冒烟（有数据库时）

可选环境变量：

```bash
export NYANYABOT_TEST_DATABASE_URI='postgres://user:pass@127.0.0.1:5432/db'
```

## 相关链接

- [nyanyabot-proto](https://github.com/xiaocaoooo/nyanyabot-proto) — 协议与插件运行时
- [AmiaBot](https://github.com/xiaocaoooo/AmiaBot) — 15 个外部插件
- English: [README.md](./README.md)

## 许可

MIT（见 workspace package 元数据）。
