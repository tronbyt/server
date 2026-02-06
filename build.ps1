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
    if (-not (Get-Command "git" -ErrorAction SilentlyContinue)) {
        Write-Error "git not found, but .git directory exists. Cannot determine version information."
        exit 1
    }
    $COMMIT = git rev-parse --short HEAD
    $VERSION = git describe --tags --always --dirty
}

Write-Host "Building Tronbyt Server ($VERSION)..." -ForegroundColor Green

# 3. Download Dependencies
go mod download

$env:CGO_ENABLED = "1"

# 4. Build 'tronbyt-server'
$LDFLAGS = "-w -s -extldflags '-static' -X 'tronbyt-server/internal/version.Version=$VERSION' -X 'tronbyt-server/internal/version.Commit=$COMMIT' -X 'tronbyt-server/internal/version.BuildDate=$DATE'"

go build -ldflags="$LDFLAGS" -tags gzip_fonts -o tronbyt-server.exe ./cmd/server

if ($LASTEXITCODE -ne 0) {
    Write-Error "Server build failed!"
    exit 1
}

# 5. Build 'migrate' (Database tool)
go build -ldflags="-w -s" -o migrate.exe ./cmd/migrate

Write-Host "Build Complete!" -ForegroundColor Green
