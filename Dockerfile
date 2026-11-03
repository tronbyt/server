FROM --platform=$BUILDPLATFORM tonistiigi/xx:1.10.0 AS xx

# hadolint global ignore=DL3018
FROM --platform=$BUILDPLATFORM golang:1.27.3-alpine AS builder
WORKDIR /app

# Install build dependencies
# ca-certificates for HTTPS/GitHub API calls
RUN apk add --no-cache git clang \
    && apk add --no-cache --upgrade ca-certificates \
    && update-ca-certificates

# Copy go mod and sum files for dependency caching
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY --from=xx / /

ARG TARGETPLATFORM
RUN xx-apk add --no-cache gcc g++ libwebp-dev libwebp-static

# Development Stage - Hot Reloading
FROM builder AS dev
CMD ["go", "tool", "air"]

# Production Build Stage
FROM builder AS build-production

# Copy source code
COPY . .

# Version Info - Keep these ARGs for build-time injection
ARG VERSION=dev
ARG COMMIT=unknown
ARG BRANCH=""
ARG REPO_URL=""
ARG BUILD_DATE=unknown

# Build all Go binaries in a single layer
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    set -x \
    && if [ -d .git ]; then \
        git config --global --add safe.directory '*' || true; \
        if [ -z "$COMMIT" ] || [ "$COMMIT" = "unknown" ]; then \
            COMMIT=$(git rev-parse HEAD 2>/dev/null || echo "unknown"); \
        fi; \
        if [ -z "$BRANCH" ]; then \
            BRANCH=$(git symbolic-ref -q --short HEAD 2>/dev/null || git rev-parse --abbrev-ref HEAD 2>/dev/null || echo ""); \
            if [ "$BRANCH" = "HEAD" ]; then BRANCH=""; fi; \
        fi; \
        if [ -z "$REPO_URL" ]; then \
            REPO_URL=$(git config --get remote.origin.url 2>/dev/null || echo ""); \
        fi; \
    fi \
    && if [ -z "$COMMIT" ] || [ "$COMMIT" = "unknown" ]; then \
        if [ -n "$REPO_URL" ] && [ -n "$BRANCH" ]; then \
            REMOTE_OUT=$(git ls-remote "$REPO_URL" "refs/heads/$BRANCH" 2>/dev/null || true); \
            for word in $REMOTE_OUT; do COMMIT="$word"; break; done; \
        fi; \
    fi \
    && CGO_ENABLED=1 xx-go build \
    -ldflags="-w -s -extldflags '-static' -X 'tronbyt-server/internal/version.Version=${VERSION}' -X 'tronbyt-server/internal/version.Commit=${COMMIT:-unknown}' -X 'tronbyt-server/internal/version.Branch=${BRANCH}' -X 'tronbyt-server/internal/version.RepoURL=${REPO_URL}' -X 'tronbyt-server/internal/version.BuildDate=${BUILD_DATE}'" \
    -tags gzip_fonts \
    -o build/app/tronbyt-server \
    ./cmd/server

WORKDIR /app/build

RUN ln -s /app/tronbyt-server boot \
    && ln -s /app/tronbyt-server app/migrate

# --- Runtime Stage ---
FROM scratch

WORKDIR /app

# Copy CA certificates from builder so TLS works in the scratch image
COPY --from=build-production /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

# Copy compiled binaries from builder
COPY --from=build-production /app/build /

# Expose port
EXPOSE 8000

# Use the Go-based entrypoint wrapper
ENTRYPOINT ["/boot"]

# Default environment variables
ENV DB_DSN=data/tronbyt.db
ENV DATA_DIR=data

# Default command to execute the main server binary
CMD ["/app/tronbyt-server"]
