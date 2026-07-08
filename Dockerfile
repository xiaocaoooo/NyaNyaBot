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

# Build the main application
RUN cd cmd/nyanyabot && go build -o /app/bin/nyanyabot .

# Runtime stage
FROM alpine:latest

# Install ca-certificates for HTTPS requests
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
