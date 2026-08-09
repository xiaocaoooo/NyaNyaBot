# Docker

Build from monorepo parent directory:

```bash
# ensure frontend export exists (also used as Docker frontend stage input)
cd NyaNyaBot/webui && pnpm build && cd ../..
docker build -f NyaNyaBot/Dockerfile -t nyanyabot .
```

Or via compose (context is monorepo parent):

```bash
cd NyaNyaBot && docker compose build && docker compose up -d
```

Run:

```bash
docker run --rm -p 3000:3000 -p 3001:3001   -v "$PWD/NyaNyaBot/data:/app/data"   -v "$PWD/NyaNyaBot/plugins:/app/plugins"   nyanyabot
```

Ports:
- `3000` WebUI
- `3001` OneBot reverse WebSocket

Notes:
- Image runs as uid/gid 10001; mounted `data/` must be writable by that user.
- Frontend stage uses Node 22 with host `webui/out` to avoid flaky Google Fonts CDN during image builds.
- Rust stage uses 1.97; runtime is debian slim non-root.
