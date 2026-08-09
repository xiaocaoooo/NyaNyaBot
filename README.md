# NyaNyaBot

Extensible chatbot **host** written in Rust (edition 2024).

NyaNyaBot loads out-of-process plugins over **local gRPC** (`nyanyabot.plugin.v1` from [`nyanyabot-proto`](https://github.com/xiaocaoooo/nyanyabot-proto)), speaks **OneBot 11** reverse WebSocket, serves a **Next.js** WebUI, and keeps the historical config JSON / REST shapes.

> Old Go / HashiCorp go-plugin binaries are **not** supported.

## Features

- Async runtime: `tokio` + `axum` Web API / static UI
- OneBot 11 reverse WebSocket (`tokio-tungstenite`)
- Plugin manager: discover `nyanyabot-plugin-*` binaries, descriptor validation, dependency order, hot configure, crash monitor + auto-restart, idle sleep, graceful shutdown
- Shared local `HostService` with per-plugin tokens (`x-nyanyabot-token`)
- Optional PostgreSQL chat log / trigger log via `sqlx`
- Built-in plugins: `builtin.status`, `external.echo`
- WebUI under `webui/` (Next.js, TypeScript), embedded with `rust-embed`

## Repository layout

```text
NyaNyaBot/
  crates/nyanyabot/                 # host library + binary
  crates/nyanyabot-plugin-builtin-status/
  crates/nyanyabot-plugin-echo/
  crates/xtask/                     # cargo xtask stage
  webui/                            # Next.js frontend
  config.example.json
  Dockerfile / docker-compose.yml
  designs/                          # design notes
```

Protocol path dependency: `../nyanyabot-proto`.

## Requirements

- Rust toolchain supporting **edition 2024** (CI/Docker use 1.97-class images)
- Optional: Node 22 + `pnpm` for WebUI rebuild
- Optional: PostgreSQL for chat/trigger logs
- Linux / Windows / macOS native builds (plugins are local processes only)

## Quick start

```bash
# 1) build host + sample plugins into ./ and ./plugins/
cargo xtask stage

# 2) prepare data dir + config
mkdir -p data plugins
cp config.example.json data/config.json   # edit as needed

# 3) run (cwd should be the NyaNyaBot workspace root)
./nyanyabot
```

Default ports (overridable in `data/config.json`):

| Port | Service |
|------|---------|
| `3000` | WebUI + REST API |
| `3001` | OneBot 11 reverse WebSocket |

Open the WebUI at `http://127.0.0.1:3000/` (password from config; default example uses WebUI password field).

### Config sketch

See `config.example.json`. Important keys:

- `onebot.reverse_ws.listen_addr`
- `webui.listen_addr` / `webui.password`
- `message_prefix` — regex; named group `content` preferred
- `plugins.<plugin_id>` — per-plugin JSON config
- `chat_log.database_uri` / `trigger_log.*` — optional PostgreSQL

Compatibility promise: existing `data/config.json` keys and REST response shapes are kept; unknown leftover keys from removed plugins are ignored.

## Plugins

### Built-in (this repo)

| Binary | Plugin ID | Role |
|--------|-----------|------|
| `nyanyabot-plugin-builtin-status` | `builtin.status` | status command |
| `nyanyabot-plugin-echo` | `external.echo` | echo test (`cmd.echo`) |

Stage copies them into `plugins/`. On Windows, binaries use the `.exe` suffix.

### External (AmiaBot)

Build and stage the [AmiaBot](https://github.com/xiaocaoooo/AmiaBot) workspace, then copy or mount its `plugins/` next to the host (or into the same `plugins/` directory the host loads).

Host discovery rules:

- Linux/macOS: executable name prefix `nyanyabot-plugin-`
- Windows: same prefix with `.exe`

Startup handshake timeout: **10 seconds**.

### Plugin handshake (summary)

1. Plugin listens on `127.0.0.1:0`
2. One readiness JSON line on stdout
3. Logs on stderr
4. Host authenticates with plugin token, then `Describe` / `AttachHost` / `Configure`

See [nyanyabot-proto](https://github.com/xiaocaoooo/nyanyabot-proto) for the full contract.

## WebUI

```bash
cd webui
pnpm install
pnpm build          # writes webui/out
```

The Rust build embeds `crates/nyanyabot/src/web/frontend` (placeholder assets exist so tests compile without a prior frontend build). For production images, copy `webui/out` into that folder or use the Docker frontend stage.

## Docker

Build **from the monorepo parent** (needs `nyanyabot-proto` + `NyaNyaBot`):

```bash
# optional but recommended: prebuild frontend export
cd NyaNyaBot/webui && pnpm build && cd ../..

docker build -f NyaNyaBot/Dockerfile -t nyanyabot .
```

Or:

```bash
cd NyaNyaBot
docker compose build && docker compose up -d
```

Run example:

```bash
docker run --rm -p 3000:3000 -p 3001:3001 \
  -v "$PWD/data:/app/data" \
  -v "$PWD/plugins:/app/plugins" \
  nyanyabot
```

Notes:

- Image user is uid/gid **10001** — mounted `data/` must be writable
- See `DOCKER.md` for more detail

## Development

```bash
cargo fmt --check
cargo clippy --workspace --all-targets -- -D warnings
cargo test --workspace
cargo xtask stage
```

Useful integration tests live under `crates/nyanyabot/tests/`:

- gRPC subprocess handshake / auth / crash restart / idle sleep
- reverse WebSocket end-to-end with a fake OneBot client
- PostgreSQL chat/trigger log smoke (when DB is available)

Environment for DB tests (optional):

```bash
export NYANYABOT_TEST_DATABASE_URI='postgres://user:pass@127.0.0.1:5432/db'
```

## Related

- [nyanyabot-proto](https://github.com/xiaocaoooo/nyanyabot-proto) — protocol + plugin runtime
- [AmiaBot](https://github.com/xiaocaoooo/AmiaBot) — 15 external plugins
- Chinese readme: [README_zh.md](./README_zh.md)

## License

MIT (workspace package metadata).
