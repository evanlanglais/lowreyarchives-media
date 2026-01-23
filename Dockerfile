# Build stage
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /app

# Copy go mod files first for caching
COPY go.mod go.sum* ./
RUN go mod download

# Copy source code
COPY . .

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-w -s" \
    -o /media-processor \
    ./cmd/processor

# Runtime stage
FROM alpine:3.19

# Install ffmpeg and imagemagick for media processing
RUN apk add --no-cache \
    ffmpeg \
    imagemagick \
    ca-certificates \
    tzdata

# Create non-root user
RUN addgroup -g 1000 processor && \
    adduser -u 1000 -G processor -D processor

# Create temp directory for processing
RUN mkdir -p /tmp/media-processor && \
    chown processor:processor /tmp/media-processor

# Copy binary from builder
COPY --from=builder /media-processor /usr/local/bin/media-processor

# Switch to non-root user
USER processor

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD wget -q --spider http://localhost:8080/healthz || exit 1

ENTRYPOINT ["/usr/local/bin/media-processor"]
