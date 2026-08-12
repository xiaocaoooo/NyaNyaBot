# NyaNyaBot 技术设计（Rust + gRPC）

## 项目目标
- 可扩展机器人框架
- 插件以独立进程运行，通过本机 gRPC 通信（`nyanyabot.plugin.v1`）
- WebUI（Next.js）管理插件与配置
- 保留 OneBot 11 反向 WebSocket、配置 JSON、PostgreSQL 表语义

## 核心组件
1. **主程序 (Rust 2024)**：`tokio` + `axum` + `sqlx` + `tokio-tungstenite`
2. **插件协议**：`../nyanyabot-proto`（tonic/prost）
3. **Web UI**：Next.js 静态导出，由 Rust build/`rust-embed` 嵌入
4. **示例插件**：`builtin.status`、`external.echo`

## 构建
```bash
cargo xtask stage
```
