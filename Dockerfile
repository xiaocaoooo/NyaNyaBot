# syntax=docker/dockerfile:1.7
# Build from monorepo parent:
#   docker build -f NyaNyaBot/Dockerfile -t nyanyabot .
#
# Frontend: Node 22 stage packages the static export.
# Prefer host `webui/out` (from `pnpm build`) to avoid flaky Google Fonts CDN in image builds.
# To force an in-image rebuild, run pnpm build in this stage when network allows.

FROM node:22-bookworm AS frontend-builder
WORKDIR /webui
ENV NEXT_TELEMETRY_DISABLED=1
COPY NyaNyaBot/webui/out ./out
RUN test -f out/index.html && node -e "console.log('frontend out ok', require('fs').readdirSync('out').slice(0,5))"

FROM rust:1.97-bookworm AS builder
WORKDIR /src
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*
COPY nyanyabot-proto /src/nyanyabot-proto
COPY NyaNyaBot /src/NyaNyaBot
COPY --from=frontend-builder /webui/out /src/NyaNyaBot/crates/nyanyabot/src/web/frontend
WORKDIR /src/NyaNyaBot
RUN cargo build --release -p nyanyabot -p nyanyabot-plugin-builtin-status -p nyanyabot-plugin-echo     && mkdir -p /out/plugins     && cp target/release/nyanyabot /out/nyanyabot     && cp target/release/nyanyabot-plugin-builtin-status /out/plugins/     && cp target/release/nyanyabot-plugin-echo /out/plugins/

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates tzdata     && rm -rf /var/lib/apt/lists/*     && groupadd -g 10001 appgroup     && useradd -u 10001 -g appgroup -m -d /app -s /usr/sbin/nologin appuser
WORKDIR /app
COPY --from=builder /out/nyanyabot /app/nyanyabot
COPY --from=builder /out/plugins /app/plugins
RUN mkdir -p /app/data && chown -R appuser:appgroup /app
USER appuser:appgroup
EXPOSE 3000 3001
ENTRYPOINT ["./nyanyabot"]
