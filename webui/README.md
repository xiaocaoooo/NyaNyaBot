# NyaNyaBot WebUI (Next.js App Router)

基于 **Next.js 14 App Router + Tailwind CSS + HeroUI** 的全新静态导出 WebUI。

## 设计目标

- `output: 'export'`，构建产物为完全静态站点（输出至 `out/`）
- 使用 CSS 变量构建 Design Token，支持一键明暗主题切换
- 响应式布局，增强键盘可达性与可见焦点样式（WCAG 2.1 AA 实践）
- 通过 Next `rewrites` 在开发模式代理后端 Go 服务

## 开发与构建

```bash
cd webui
npm install
npm run dev
```

默认开发端口：`3100`

### 后端代理

在 `webui/.env.local` 中配置：

```bash
NEXT_PUBLIC_BACKEND_ORIGIN=http://127.0.0.1:3000
```

开发模式下会将以下请求反代至 Go 后端：

- `/api/:path*` -> `${NEXT_PUBLIC_BACKEND_ORIGIN}/api/:path*`
- `/assets/:path*` -> `${NEXT_PUBLIC_BACKEND_ORIGIN}/assets/:path*`

## 生产构建

```bash
npm run build
```

构建后静态文件位于 `webui/out`。

## 替换 Go WebUI（已接入）

当前仓库中的 Go 服务已改为直接托管 Next.js 静态导出文件：

- 托管目录：`internal/web/frontend`
- 嵌入入口：`internal/web/frontend_assets.go`
- 服务逻辑：`internal/web/server.go`（API + 静态文件）

同步导出到 Go 目录：

```bash
./scripts/sync_webui_export.sh
```

## 目录结构

```text
webui
├─ app
│  ├─ config/page.tsx
│  ├─ plugins/page.tsx
│  ├─ globals.css
│  ├─ layout.tsx
│  ├─ not-found.tsx
│  ├─ page.tsx
│  └─ template.tsx
├─ components
│  ├─ layout
│  │  ├─ app-shell.tsx
│  │  └─ main-nav.tsx
│  ├─ motion/page-transition.tsx
│  ├─ providers/app-providers.tsx
│  ├─ screens
│  │  ├─ config-screen.tsx
│  │  ├─ dashboard-screen.tsx
│  │  └─ plugins-screen.tsx
│  ├─ theme/theme-toggle.tsx
│  └─ ui
│     ├─ button.tsx
│     ├─ card.tsx
│     ├─ form-field.tsx
│     ├─ index.ts
│     ├─ input.tsx
│     ├─ status-message.tsx
│     └─ textarea.tsx
├─ lib
│  ├─ api
│  │  ├─ client.ts
│  │  └─ types.ts
│  └─ utils/cn.ts
├─ .env.example
├─ next.config.js
├─ postcss.config.js
├─ tailwind.config.ts
├─ tsconfig.json
└─ package.json
```
