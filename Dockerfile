# syntax=docker/dockerfile:1.7
# Build from monorepo parent (BuildKit required):
#   docker build -f NyaNyaBot/Dockerfile -t nyanyabot .
#
# Frontend is built inside the image (pnpm). Build needs network for
# npm registry and Google Fonts (next/font/google).
# Rust deps use cargo-chef layering + BuildKit cache mounts.
# crates/nyanyabot/build.rs embeds webui/out (or frontend-placeholder).

FROM node:22-bookworm AS frontend-builder
WORKDIR /webui
ENV NEXT_TELEMETRY_DISABLED=1
RUN corepack enable && corepack prepare pnpm@9.15.9 --activate
COPY NyaNyaBot/webui/package.json NyaNyaBot/webui/pnpm-lock.yaml ./
RUN --mount=type=cache,target=/root/.local/share/pnpm/store,id=nyanyabot-pnpm-store,sharing=locked \
    pnpm install --frozen-lockfile
COPY NyaNyaBot/webui/ ./
RUN --mount=type=cache,target=/webui/.next/cache,id=nyanyabot-next-cache,sharing=locked \
    pnpm build \
 && test -f out/index.html \
 && test -f out/plugins/index.html

FROM rust:1.97-bookworm AS chef
WORKDIR /src
RUN --mount=type=cache,target=/usr/local/cargo/registry,id=nyanyabot-cargo-registry,sharing=locked \
    --mount=type=cache,target=/usr/local/cargo/git,id=nyanyabot-cargo-git,sharing=locked \
    cargo install cargo-chef --locked --version 0.1.72

FROM chef AS planner
COPY nyanyabot-proto /src/nyanyabot-proto
COPY NyaNyaBot /src/NyaNyaBot
WORKDIR /src/NyaNyaBot
RUN cargo chef prepare --recipe-path /recipe.json

FROM chef AS builder
WORKDIR /src
RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,target=/var/lib/apt,sharing=locked \
    rm -f /etc/apt/apt.conf.d/docker-clean \
 && apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates

# Path dep must exist during cook (workspace path = ../nyanyabot-proto).
COPY nyanyabot-proto /src/nyanyabot-proto
COPY --from=planner /recipe.json /src/NyaNyaBot/recipe.json
WORKDIR /src/NyaNyaBot
RUN --mount=type=cache,target=/usr/local/cargo/registry,id=nyanyabot-cargo-registry,sharing=locked \
    --mount=type=cache,target=/usr/local/cargo/git,id=nyanyabot-cargo-git,sharing=locked \
    --mount=type=cache,target=/src/NyaNyaBot/target,id=nyanyabot-cargo-target,sharing=locked \
    cargo chef cook --release --recipe-path recipe.json \
      -p nyanyabot \
      -p nyanyabot-plugin-builtin-status \
      -p nyanyabot-plugin-echo

COPY NyaNyaBot /src/NyaNyaBot
COPY --from=frontend-builder /webui/out /src/NyaNyaBot/webui/out
# build.rs copies webui/out -> crates/nyanyabot/generated/frontend (source tree, not
# target/). With a persistent target cache mount, Cargo may skip build.rs when its
# inputs look unchanged, leaving generated/ missing in a fresh container layer.
# Touch build.rs so rust-embed always sees the folder.
RUN --mount=type=cache,target=/usr/local/cargo/registry,id=nyanyabot-cargo-registry,sharing=locked \
    --mount=type=cache,target=/usr/local/cargo/git,id=nyanyabot-cargo-git,sharing=locked \
    --mount=type=cache,target=/src/NyaNyaBot/target,id=nyanyabot-cargo-target,sharing=locked \
    test -f webui/out/index.html \
 && test -f webui/out/plugins/index.html \
 && touch crates/nyanyabot/build.rs \
 && cargo build --release \
      -p nyanyabot \
      -p nyanyabot-plugin-builtin-status \
      -p nyanyabot-plugin-echo \
 && mkdir -p /out/plugins \
 && cp target/release/nyanyabot /out/nyanyabot \
 && cp target/release/nyanyabot-plugin-builtin-status /out/plugins/ \
 && cp target/release/nyanyabot-plugin-echo /out/plugins/

FROM debian:bookworm-slim
RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,target=/var/lib/apt,sharing=locked \
    rm -f /etc/apt/apt.conf.d/docker-clean \
 && apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates tzdata \
 && groupadd -g 10001 appgroup \
 && useradd -u 10001 -g appgroup -m -d /app -s /usr/sbin/nologin appuser
WORKDIR /app
COPY --from=builder /out/nyanyabot /app/nyanyabot
COPY --from=builder /out/plugins /app/plugins
RUN mkdir -p /app/data && chown -R appuser:appgroup /app
USER appuser:appgroup
EXPOSE 3000 3001
ENTRYPOINT ["./nyanyabot"]
