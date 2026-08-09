# NyaNyaBot Agent Notes

## Stack
- Rust 2024 workspace
- Plugin transport: gRPC via `nyanyabot-proto` (`nyanyabot.plugin.v1`)
- WebUI: `webui/` Next.js (TypeScript) retained
- DB: PostgreSQL via `sqlx` (chat/trigger logs)

## Layout
- `crates/nyanyabot` host
- `crates/nyanyabot-plugin-builtin-status`
- `crates/nyanyabot-plugin-echo`
- `crates/xtask` (`cargo xtask stage`)
- protocol path dep: `../nyanyabot-proto`

## Plugin handshake
1. Plugin listens on `127.0.0.1:0`
2. Prints one readiness JSON line on stdout: `{protocol_version, addr, token}`
3. Logs go to stderr
4. Host connects with `x-nyanyabot-token`, calls Describe/AttachHost/Configure

## Compatibility kept
- config JSON keys, REST API shapes, OneBot 11 behavior, plugin IDs, listener IDs, export names, binary names `nyanyabot-plugin-*`

## Not compatible
- Old Go / hashicorp go-plugin binaries

## Checks
```bash
cargo fmt --check
cargo clippy --workspace --all-targets -- -D warnings
cargo test --workspace
cargo xtask stage
```
