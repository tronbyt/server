FROM --platform=$BUILDPLATFORM tonistiigi/xx:1.9.0 AS xx

# hadolint global ignore=DL3018
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS builder
WORKDIR /app

# Install build dependencies
# build-base for CGo
# ca-certificates for HTTPS/GitHub API calls
# libwebp-dev for headers (needed at build time)
# libwebp-static for static linking
# git for go mod download
RUN apk add --no-cache git clang

# Copy go mod and sum files for dependency caching
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY --from=xx / /

ARG TARGETPLATFORM
RUN xx-apk add --no-cache gcc g++ libwebp-dev libwebp-static

# Development Stage - Hot Reloading
FROM builder AS dev
RUN go install github.com/air-verse/air
CMD ["air"]

# Production Build Stage
FROM builder AS build-production

# Copy source code
COPY . .

# Version Info - Keep these ARGs for build-time injection
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

# Build all Go binaries in a single layer
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 xx-go build -ldflags="-w -s -extldflags '-static'" -o boot ./cmd/boot && \
    CGO_ENABLED=1 xx-go build -ldflags="-w -s -extldflags '-static' -X 'tronbyt-server/internal/version.Version=${VERSION}' -X 'tronbyt-server/internal/version.Commit=${COMMIT}' -X 'tronbyt-server/internal/version.BuildDate=${BUILD_DATE}'" -tags gzip_fonts -o tronbyt-server ./cmd/server && \
    CGO_ENABLED=1 xx-go build -ldflags="-w -s -extldflags '-static'" -o migrate ./cmd/migrate

# --- Runtime Stage ---
FROM scratch

WORKDIR /app

# Copy CA certificates from builder so TLS works in the scratch image
COPY --from=build-production /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

# Copy compiled binaries from builder
COPY --from=build-production /app/boot /boot
COPY --from=build-production /app/tronbyt-server /app/tronbyt-server
COPY --from=build-production /app/migrate /app/migrate

# Expose port
EXPOSE 8000

# Use the Go-based entrypoint wrapper
ENTRYPOINT ["/boot"]

# Default environment variables
ENV DB_DSN=data/tronbyt.db
ENV DATA_DIR=data

# Default command to execute the main server binary
CMD ["/app/tronbyt-server"]
