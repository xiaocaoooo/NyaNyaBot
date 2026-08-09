# Docker

Build from the monorepo parent (needs sibling `nyanyabot-proto`).

```bash
# 1) export WebUI (required for a real console image; must include /plugins)
cd NyaNyaBot/webui && pnpm build && cd ../..
# or: cd NyaNyaBot && cargo xtask frontend

# 2) image
docker build -f NyaNyaBot/Dockerfile -t nyanyabot .

# 3) run
docker run --rm -p 3000:3000 -p 3001:3001 \
  -v "$PWD/NyaNyaBot/data:/app/data" \
  -v "$PWD/NyaNyaBot/plugins:/app/plugins" \
  nyanyabot
```

## Notes

- Ports: `3000` WebUI, `3001` OneBot reverse WebSocket.
- Frontend stage copies host `webui/out` into the image build context path expected by `crates/nyanyabot/build.rs` (`webui/out`). The Rust build embeds that export; it does **not** commit static files under `src/web/frontend`.
- If `webui/out` is missing, `build.rs` falls back to `frontend-placeholder` (enough to compile/tests, not a full console).
- Dockerfile requires `out/plugins/index.html` so `/plugins` cannot ship broken.
