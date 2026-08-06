# Frontend build stage
FROM node:20-alpine AS frontend-builder

WORKDIR /app

# Copy frontend source
COPY webui ./webui

# Install dependencies and build
WORKDIR /app/webui
RUN npm install && npm run build

# Build stage
FROM golang:1.25.6-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git

WORKDIR /app

# Copy go.mod and go.sum and download dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy the source code
COPY . .

# Copy the built frontend assets from the frontend-builder stage
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

# Copy the binary from the builder stage
COPY --from=builder /app/bin/nyanyabot .

# Create necessary directories for data and plugins, and assign ownership
RUN mkdir -p /app/data /app/plugins && \
    chown -R appuser:appgroup /app

USER appuser:appgroup

# Expose WebUI and OneBot Reverse WS ports
EXPOSE 3000 3001

# Run the application
ENTRYPOINT ["./nyanyabot"]
