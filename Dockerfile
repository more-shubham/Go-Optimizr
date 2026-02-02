# ============================================
# Stage 1: Build stage using Golang Alpine
# ============================================
FROM golang:1.22-alpine AS builder

# Install build dependencies for CGO (required by webp library)
RUN apk add --no-cache \
    gcc \
    musl-dev \
    libwebp-dev \
    libwebp-tools

# Set working directory
WORKDIR /app

# Copy go mod files first for better layer caching
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the application with CGO enabled
# -ldflags="-s -w" strips debug info for smaller binary
RUN CGO_ENABLED=1 GOOS=linux go build \
    -ldflags="-s -w -linkmode external -extldflags '-static'" \
    -o go-optimizr ./cmd/optimizr

# ============================================
# Stage 2: Runtime stage using minimal Alpine
# ============================================
FROM alpine:3.19 AS runtime

# Install runtime dependencies for WebP
RUN apk add --no-cache \
    libwebp \
    ca-certificates \
    && rm -rf /var/cache/apk/*

# Create non-root user for security
RUN addgroup -g 1000 optimizr && \
    adduser -u 1000 -G optimizr -s /bin/sh -D optimizr

# Set working directory
WORKDIR /app

# Copy the binary from builder
COPY --from=builder /app/go-optimizr /app/go-optimizr

# Create directories for input/output with proper permissions
RUN mkdir -p /data/input /data/output && \
    chown -R optimizr:optimizr /data /app

# Switch to non-root user
USER optimizr

# Default environment variables
ENV INPUT_DIR=/data/input \
    OUTPUT_DIR=/data/output \
    WORKERS=3 \
    QUALITY=80

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD pgrep go-optimizr || exit 1

# Entry point with configurable parameters
ENTRYPOINT ["/app/go-optimizr"]
CMD ["-input", "/data/input", "-output", "/data/output", "-workers", "3", "-quality", "80", "-verbose"]
