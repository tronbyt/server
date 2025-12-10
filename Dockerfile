# hadolint global ignore=DL3018
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Install build dependencies
# build-base for CGo
# ca-certificates for HTTPS/GitHub API calls
# libwebp-dev for headers (needed at build time)
# libwebp-static for static linking
# git for go mod download
RUN apk add --no-cache git build-base libwebp-dev libwebp-static

# Copy go mod and sum files for dependency caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Version Info - Keep these ARGs for build-time injection
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

# Build the Go-based entrypoint wrapper to manage permissions and drop privileges
RUN CGO_ENABLED=0 go build -ldflags="-w -s -extldflags '-static'" -o boot ./cmd/boot

# Build the main application and supporting binaries
# CGO_ENABLED=1 is required for pixlet/go-libwebp and go-sqlite3
RUN CGO_ENABLED=1 go build -ldflags="-w -s -extldflags '-static' -X 'tronbyt-server/internal/version.Version=${VERSION}' -X 'tronbyt-server/internal/version.Commit=${COMMIT}' -X 'tronbyt-server/internal/version.BuildDate=${BUILD_DATE}'" -tags gzip_fonts -o tronbyt-server ./cmd/server && \
    CGO_ENABLED=1 go build -ldflags="-w -s -extldflags '-static'" -o migrate ./cmd/migrate

# --- Runtime Stage ---
FROM scratch

WORKDIR /app

# Copy CA certificates from builder so TLS works in the scratch image
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

# Copy compiled binaries from builder
COPY --from=builder /app/boot /boot
COPY --from=builder /app/tronbyt-server /app/tronbyt-server
COPY --from=builder /app/migrate /app/migrate

# Expose port
EXPOSE 8000

# Use the Go-based entrypoint wrapper
ENTRYPOINT ["/boot"]

# Default command to execute the main server binary
CMD ["/app/tronbyt-server", "-db", "data/tronbyt.db", "-data", "data"]
