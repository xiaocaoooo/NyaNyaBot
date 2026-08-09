# syntax=docker/dockerfile:1.7
# Build from monorepo parent:
#   docker build -f NyaNyaBot/Dockerfile -t nyanyabot .
#
# Frontend: prefer host `NyaNyaBot/webui/out` (from `pnpm build` / `cargo xtask frontend`)
# so the image build does not depend on flaky Google Fonts CDN. The Rust crate build.rs
# embeds webui/out into the binary (or frontend-placeholder if out is missing).

FROM node:22-bookworm AS frontend-builder
WORKDIR /webui
ENV NEXT_TELEMETRY_DISABLED=1
# Prebuilt export from the build context (run pnpm build on the host first).
COPY NyaNyaBot/webui/out ./out
RUN test -f out/index.html \
  && test -f out/plugins/index.html \
  && node -e "console.log('frontend out ok', require('fs').readdirSync('out').slice(0,8))"

FROM rust:1.97-bookworm AS builder
WORKDIR /src
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates \
  && rm -rf /var/lib/apt/lists/*
COPY nyanyabot-proto /src/nyanyabot-proto
COPY NyaNyaBot /src/NyaNyaBot
# Place export where crates/nyanyabot/build.rs expects it: ../../webui/out
COPY --from=frontend-builder /webui/out /src/NyaNyaBot/webui/out
WORKDIR /src/NyaNyaBot
RUN cargo build --release -p nyanyabot -p nyanyabot-plugin-builtin-status -p nyanyabot-plugin-echo \
  && mkdir -p /out/plugins \
  && cp target/release/nyanyabot /out/nyanyabot \
  && cp target/release/nyanyabot-plugin-builtin-status /out/plugins/ \
  && cp target/release/nyanyabot-plugin-echo /out/plugins/

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates tzdata \
  && rm -rf /var/lib/apt/lists/* \
  && groupadd -g 10001 appgroup \
  && useradd -u 10001 -g appgroup -m -d /app -s /usr/sbin/nologin appuser
WORKDIR /app
COPY --from=builder /out/nyanyabot /app/nyanyabot
COPY --from=builder /out/plugins /app/plugins
RUN mkdir -p /app/data && chown -R appuser:appgroup /app
USER appuser:appgroup
EXPOSE 3000 3001
ENTRYPOINT ["./nyanyabot"]
