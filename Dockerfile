# Frontend build stage
FROM node:22-alpine AS frontend-builder

WORKDIR /app/webui

RUN corepack enable

# Copy lockfiles and install dependencies
COPY webui/package.json webui/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile --ignore-scripts

# Copy full webui source and build
COPY webui/ ./
RUN pnpm run build

COPY webui/ ./

RUN npm run build

FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download && go mod verify

COPY . .

COPY --from=frontend-builder /app/webui/out /app/internal/web/frontend

# Build the main application
RUN cd cmd/nyanyabot && go build -o /app/bin/nyanyabot .

# Runtime stage
FROM alpine:latest

# Install ca-certificates for timezone data and HTTPS requests
RUN apk add --no-cache ca-certificates tzdata

# Create dedicated non-root group and user with fixed IDs for best security engineering practices
RUN addgroup -g 10001 -S appgroup && \
    adduser -u 10001 -S -G appgroup -h /app -s /sbin/nologin appuser

WORKDIR /app
COPY --from=builder /app/bin/nyanyabot .

# Create necessary directories for data and plugins, and assign ownership
RUN mkdir -p /app/data /app/plugins && \
    chown -R appuser:appgroup /app

USER appuser:appgroup

# Expose WebUI and OneBot Reverse WS ports
EXPOSE 3000 3001
ENTRYPOINT ["./nyanyabot"]