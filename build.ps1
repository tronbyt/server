Write-Host "Checking dependencies..." -ForegroundColor Cyan
if (-not (Get-Command "gcc" -ErrorAction SilentlyContinue)) {
    Write-Error "GCC not found. Please ensure MSYS2 MinGW64 bin folder is in your PATH."
    exit 1
}

# 1. Setup Environment Variables
$env:GOOS = "windows"
$env:GOARCH = "amd64"
$env:CGO_ENABLED = "1" # Required for libwebp and sqlite

# 2. Get Version Info
$VERSION = "dev"
$COMMIT = "unknown"
$DATE = Get-Date -Format "yyyy-MM-dd"

if (Test-Path .git) {
    $COMMIT = git rev-parse --short HEAD
    $VERSION = git describe --tags --always --dirty
}

Write-Host "Building Tronbyt Server ($VERSION)..." -ForegroundColor Green

# 3. Download Dependencies
go mod download

# 4. Build 'boot' (Entrypoint wrapper)
# Note: Usually on Windows you don't need this wrapper, but building it just in case.
# Boot is CGO-free (CGO_ENABLED=0), so we toggle it off briefly.
$env:CGO_ENABLED = "0"
go build -ldflags="-w -s" -o boot.exe ./cmd/boot
$env:CGO_ENABLED = "1"

# 5. Build 'tronbyt-server'
$LDFLAGS = "-w -s -extldflags '-static' -X 'tronbyt-server/internal/version.Version=$VERSION' -X 'tronbyt-server/internal/version.Commit=$COMMIT' -X 'tronbyt-server/internal/version.BuildDate=$DATE'"

go build -ldflags="$LDFLAGS" -tags gzip_fonts -o tronbyt-server.exe ./cmd/server

if ($LASTEXITCODE -ne 0) {
    Write-Error "Server build failed!"
    exit 1
}

# 6. Build 'migrate' (Database tool)
go build -ldflags="-w -s" -o migrate.exe ./cmd/migrate

Write-Host "Build Complete!" -ForegroundColor Green
Write-Host "Run the server using: .\start.bat" -ForegroundColor Yellow