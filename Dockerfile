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

WORKDIR /app
COPY --from=builder /app/bin/nyanyabot .
RUN mkdir -p /app/data /app/plugins && \
    chown -R appuser:appgroup /app
USER appuser
EXPOSE 3000 3001
ENTRYPOINT ["./nyanyabot"]