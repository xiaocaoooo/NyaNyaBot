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
RUN mkdir -p internal/web/frontend && cp -R /app/webui/out/. internal/web/frontend/
# Note: we use a separate COPY or RUN to ensure the directory exists for //go:embed

# Build the main application
RUN cd cmd/nyanyabot && go build -o /app/bin/nyanyabot .

# Runtime stage
FROM alpine:latest

# Install ca-certificates for timezone data and HTTPS requests
RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

# Copy the binary from the builder stage
COPY --from=builder /app/bin/nyanyabot .

# Create necessary directories for data and plugins
RUN mkdir -p /app/data /app/plugins

# Expose WebUI and OneBot Reverse WS ports
EXPOSE 3000 3001

# Run the application
ENTRYPOINT ["./nyanyabot"]
