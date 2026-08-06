FROM node:20-alpine AS frontend-builder

WORKDIR /app/webui

COPY webui/package*.json ./

RUN npm ci --production=false

COPY webui/ ./

RUN npm run build

FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download && go mod verify

COPY . .

COPY --from=frontend-builder /app/webui/out /app/internal/web/frontend

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-s -w -trimpath" \
    -o /app/bin/nyanyabot \
    ./cmd/nyanyabot

FROM alpine:3.21 AS runtime-alpine
RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -g 1000 -S appgroup && \
    adduser -u 1000 -S appuser -G appgroup
WORKDIR /app
COPY --from=builder /app/bin/nyanyabot .
RUN mkdir -p /app/data /app/plugins && \
    chown -R appuser:appgroup /app
USER appuser
EXPOSE 3000 3001
ENTRYPOINT ["./nyanyabot"]