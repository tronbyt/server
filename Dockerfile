# Build stage
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Install build dependencies
# git for go mod download
# build-base for CGO (required by pixlet dependencies like go-libwebp)
RUN apk add --no-cache git build-base libwebp-dev

# Copy go mod and sum files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Version Info
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

# Build the application
# CGO_ENABLED=1 is required for pixlet/libwebp
# -ldflags="-w -s" reduces binary size
RUN CGO_ENABLED=1 go build -tags netgo,osusergo,musl -ldflags="-w -s -X 'tronbyt-server/internal/version.Version=${VERSION}' -X 'tronbyt-server/internal/version.Commit=${COMMIT}' -X 'tronbyt-server/internal/version.BuildDate=${BUILD_DATE}'" -o tronbyt-server ./cmd/server && \
    CGO_ENABLED=1 go build -tags musl -ldflags="-w -s" -o migrate ./cmd/migrate

# Runtime stage
# We can use a minimal alpine image (or scratch, but alpine is safer for debugging)
FROM alpine:latest

WORKDIR /app

# Install runtime dependencies
# ca-certificates for HTTPS
# shadow for usermod/groupmod
# su-exec for step-down from root
# libwebp, libwebpmux, libwebpdemux, libsharpyuv for dynamic linking
RUN apk add --no-cache ca-certificates shadow su-exec libwebp libwebpmux libwebpdemux libsharpyuv

# Create a non-root user
RUN addgroup -S -g 1000 tronbyt && adduser -S -u 1000 -G tronbyt tronbyt

# Copy entrypoint script
COPY docker-entrypoint.sh /docker-entrypoint.sh
RUN chmod +x /docker-entrypoint.sh

# Copy binary from builder
COPY --from=builder /app/tronbyt-server .
COPY --from=builder /app/migrate .

# Create data directory
RUN mkdir -p data

# Expose port
EXPOSE 8000

# Define entrypoint
ENTRYPOINT ["/docker-entrypoint.sh"]

# Run the server
CMD ["./tronbyt-server", "-db", "data/tronbyt.db", "-data", "data"]
