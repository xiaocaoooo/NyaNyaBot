# Docker

Build from the monorepo parent (needs sibling `nyanyabot-proto`). **BuildKit** is required
(Docker 23+ / Engine 29 defaults are fine).

```bash
# image (WebUI is built inside the Dockerfile via pnpm)
docker build -f NyaNyaBot/Dockerfile -t nyanyabot .

# or compose (context is already the monorepo parent)
cd NyaNyaBot && docker compose build && docker compose up -d
```

Run:

```bash
docker run --rm -p 3000:3000 -p 3001:3001 \
  -v "$PWD/NyaNyaBot/data:/app/data" \
  -v "$PWD/NyaNyaBot/plugins:/app/plugins" \
  nyanyabot
```

## Cache layout

- **Frontend**: `package.json` / `pnpm-lock.yaml` layer + pnpm store cache mount; source changes redo `pnpm build` only.
- **Rust**: `cargo-chef` `prepare` → `cook` (deps) → `cargo build` (app). Registry/git/target use BuildKit cache mounts so dependency work survives source-only rebuilds.
- **Context**: monorepo root `.dockerignore` excludes `AmiaBot/`, `**/target/`, `webui/out`, etc. (`NyaNyaBot/.dockerignore` is **not** used when context is the parent).

## Notes

- Ports: `3000` WebUI, `3001` OneBot reverse WebSocket.
- Image build needs network: npm registry, crates.io (first/cold), and **Google Fonts** (`next/font/google` in WebUI).
- Frontend stage runs `pnpm build` and requires `out/index.html` + `out/plugins/index.html`.
- `crates/nyanyabot/build.rs` embeds `webui/out` into the binary. Local non-Docker builds can still use host `pnpm build` / `cargo xtask frontend`; if `webui/out` is missing, `frontend-placeholder` is embedded (compile/tests only, not a full console).
- Image ships host + `nyanyabot-plugin-builtin-status` + `nyanyabot-plugin-echo` only (not AmiaBot plugins).
- Dockerfile touches `crates/nyanyabot/build.rs` before `cargo build` so `generated/frontend` is always produced under a cached `target/` mount (rust-embed reads the source tree, not `OUT_DIR`).
