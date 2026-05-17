# =============================================================================
# VedaDB API Manager (VAPIM) - Multi-stage Docker Build
# =============================================================================

# ---------------------------------------------------------------------------
# Stage 1: Build
# ---------------------------------------------------------------------------
FROM golang:1.21-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git ca-certificates tzdata make

# Set working directory
WORKDIR /build

# Copy go module files first for better caching
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download && go mod verify

# Copy source code
COPY . .

# Build arguments
ARG BUILD_TIME
ARG COMMIT_HASH
ARG VERSION=1.0.0

# Build the binary with optimizations
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-s -w \
    -X main.Version=${VERSION} \
    -X main.BuildTime=${BUILD_TIME} \
    -X main.CommitHash=${COMMIT_HASH}" \
    -a -installsuffix cgo \
    -o bin/vapim \
    ./cmd/apim

# ---------------------------------------------------------------------------
# Stage 2: Security Scan (optional)
# ---------------------------------------------------------------------------
FROM aquasec/trivy:latest AS scanner
COPY --from=builder /build/bin/vapim /scan/vapim
# Run security scan (non-blocking)
RUN trivy filesystem --exit-code 0 --no-progress /scan/ || true

# ---------------------------------------------------------------------------
# Stage 3: Production
# ---------------------------------------------------------------------------
FROM scratch AS production

# Metadata labels
LABEL org.opencontainers.image.title="VedaDB API Manager" \
      org.opencontainers.image.description="High-Performance API Gateway built in Go" \
      org.opencontainers.image.version="1.0.0" \
      org.opencontainers.image.vendor="VedaDB" \
      org.opencontainers.image.licenses="Apache-2.0"

# Copy CA certificates from builder
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Copy timezone data
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo

# Copy binary from builder
COPY --from=builder /build/bin/vapim /app/vapim

# Copy default config
COPY --from=builder /build/configs/config.yaml /app/configs/config.yaml

# Create non-root user (using UID since scratch has no adduser)
# We use a high UID to avoid conflicts
USER 65534:65534

# Set working directory
WORKDIR /app

# Expose ports
# 9443 - Gateway API
# 9444 - Key Manager
# 9445 - Publisher API
# 9090 - Prometheus metrics (internal)
EXPOSE 9443 9444 9445

# Environment variables
ENV VAPIM_LOG_LEVEL=info \
    VAPIM_CONFIG_PATH=/app/configs/config.yaml \
    VAPIM_DATABASE_HOST=host.docker.internal \
    VAPIM_DATABASE_PORT=6380

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD ["/app/vapim", "-version"] || exit 1

# Run the binary
ENTRYPOINT ["/app/vapim"]
CMD ["--config", "/app/configs/config.yaml"]

# ---------------------------------------------------------------------------
# Stage 4: Development
# ---------------------------------------------------------------------------
FROM golang:1.21-alpine AS development

# Install development tools
RUN apk add --no-cache git ca-certificates tzdata make curl

# Install air for hot reload
RUN go install github.com/cosmtrek/air@latest

WORKDIR /app

# Copy go module files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Expose ports
EXPOSE 9443 9444 9445

# Run with air for hot reload
CMD ["air", "-c", ".air.toml"]
