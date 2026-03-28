# NyaNyaBot 技术设计

## 项目目标
- 构建一个可扩展的机器人框架
- 支持插件系统（安装/卸载 无需重启）
- 插件可暴露函数供主程序调用
- 提供 Web UI 管理插件及配置
- 使用 go-plugin 与 TemplUI 实现

## 核心组件

1. **主程序 (Go)**
   - 使用 Go 1.20+
   - 负责加载/卸载插件、路由调用
   - 管理插件生命周期
   - 提供 RPC 或 gRPC 接口与插件通信

2. **插件系统**
   - 基于 [HashiCorp go-plugin](https://github.com/hashicorp/go-plugin)
   - 插件以独立进程运行，通过 gRPC 或 net/rpc 通信
   - 插件实现统一接口，例如 `Plugin` 包含 `Init`, `Execute`, `Shutdown` 等方法
   - 插件可动态下载、编译（可选）并安装到 `plugins/` 目录
   - 支持插件的热插拔：加载时通过 go-plugin 启动子进程，卸载时优雅停止

3. **Web UI**
   - 使用 [TemplUI](https://github.com/ing-bank/templui) 或类似 Go 原生组件构建
   - 提供管理界面：查看已安装插件、启用/禁用、配置参数
   - 展示日志、调用结果以及机器人运行状态
   - RESTful 后端由主程序提供

4. **扩展点**
   - 事件系统：插件可监听机器人事件（消息、命令等）
   - 命令注册：插件可注册命令字
   - RPC 调用：主程序向插件请求处理某些任务
   - 定时任务：插件可注册 cron 风格定时任务

5. **依赖管理**
   - Go Modules
   - Web 资源静态打包（go:embed 或 TemplUI 内置）

6. **目录结构建议**
   ```
   /cmd/nyanyabot/main.go       # 主程序入口
   /internal/                   # 核心逻辑
     /plugin/                   # 插件加载/管理模块
     /web/                      # web ui 相关
     /core/                     # 机器人核心逻辑
   /plugins/                    # 存放插件二进制或源码
   /designs/tech_stack.md       # 当前文档
   ```

## 插件开发流程
- 编写实现指定接口的 Go 包
- 使用 go-plugin 框架打包为可执行文件
- 将插件复制到 `plugins/` 并在 Web UI 上安装

## 插件配置

- 插件在启动握手 `Descriptor` 中可声明 `config`（JSON Schema + default）。
- 主程序在加载插件后立刻下发配置：`Configure(config_json)`。
- 配置保存在 `data/config.json` 的 `plugins.{plugin_id}` 下，Web UI 后续可提供编辑入口。

## 测试用示例插件

### ConfigDump（用于验证“配置热更新”）

- 源码：`cmd/nyanyabot-plugin-configdump/main.go`
- 插件入口：`plugins/nyanyabot-plugin-configdump`（可执行脚本，便于直接被主程序扫描加载）
- 指令：
   - `/cfg`：返回当前运行中的配置 JSON
   - `/cfg pretty`：返回格式化后的配置 JSON
- 验证点：在 WebUI 的 `/plugins/external.configdump/config` 修改 `prefix` 后，无需重启，下一次 `/cfg` 即可看到变化

## 熔断与安全
- 插件运行在隔离进程，避免崩溃影响主程序
- 可以限制插件资源与权限

## 下一步
- 编写框架骨架代码
- 实现简单的示例插件
- 完成 Web UI 样式和管理页面
- 设计并落地插件接口：见 `designs/plugin_interface.md`

## 接口设计文档
- NapCat / OneBot 11 适配层接口设计：见 `designs/interface_design.md`

## 插件接口设计文档
- 插件元信息/命令/事件监听与分发：见 `designs/plugin_interface.md`

> 🚀 此文档作为初步设计，可根据需求迭代
