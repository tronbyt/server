# hadolint global ignore=DL3018
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Install build dependencies
# git for go mod download
# build-base for CGO (required by pixlet dependencies like go-libwebp)
# libwebp-dev for dynamic linking (needed at build time)
RUN apk add --no-cache git build-base libwebp-dev

# Copy go mod and sum files for dependency caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Version Info - Keep these ARGs for build-time injection
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

# Build the Go-based entrypoint wrapper (pure Go, statically linked for minimal footprint)
# This binary will manage permissions and drop privileges
RUN CGO_ENABLED=0 go build -ldflags="-w -s" -o boot ./cmd/boot

# Build the main application (tronbyt-server)
# CGO_ENABLED=1 is required for pixlet/go-libwebp (dynamic linking)
# LDFLAGS injects version information from ARGs
RUN CGO_ENABLED=1 go build -ldflags="-w -s -X 'tronbyt-server/internal/version.Version=${VERSION}' -X 'tronbyt-server/internal/version.Commit=${COMMIT}' -X 'tronbyt-server/internal/version.BuildDate=${BUILD_DATE}'" -o tronbyt-server ./cmd/server && \
    CGO_ENABLED=1 go build -ldflags="-w -s" -o migrate ./cmd/migrate

# --- Runtime Stage ---
FROM alpine:3.23

WORKDIR /app

# Install runtime dependencies
# ca-certificates for HTTPS/GitHub API calls
# libwebp, libwebpmux, libwebpdemux, libsharpyuv for dynamic linking
RUN apk add --no-cache ca-certificates libwebpmux libwebpdemux libsharpyuv

# Copy compiled binaries from builder
COPY --from=builder /app/boot /boot
COPY --from=builder /app/tronbyt-server /app/tronbyt-server
COPY --from=builder /app/migrate /app/migrate

# Create data directory (permissions handled by /boot wrapper)
RUN mkdir -p data

# Expose port
EXPOSE 8000

# Use the Go-based entrypoint wrapper
ENTRYPOINT ["/boot"]

# Default command to execute the main server binary
CMD ["/app/tronbyt-server", "-db", "data/tronbyt.db", "-data", "data"]
