# Tronbyt Server
[Tronbyt Demo](https://tronbyt.clodhost.com/auth/register)

The Tronbyt Server is a Go-based application designed to manage apps on Tronbyt devices locally, without relying on Tidbyt's backend servers. It offers a web UI for app discoverability and operates independently of cloud dependencies, ensuring continued functionality even if Tidbyt's servers are offline. The server also enables some APIs that were previously blocked by Tidbyt's servers, such as Surfline apps, and supports custom hardware.

However, there are some drawbacks, including the lack of a mobile app, slightly higher latency for notifications, and limited support for some built-in apps and apps relying on Tidbyt's cloud services for secrets.

**Prerequisites for running Tronbyt Server:**
*   **Docker** and **Docker Compose** (optional but recommended) for containerized deployment.
*   **Homebrew** for macOS and Linux bare-metal installations (provides pre-built binary).
*   **Go 1.26+** if building from source.

**Supported Devices:**
*   Tidbyt Gen1 and Gen2
*   Tronbyt S3 and S3 Wide
*   MatrixPortal S3, MatrixPortal S3 Waveshare, MatrixPortal S3 Wide (128x64) and MatrixPortal S3 Square (64x64)
*   Raspberry Pi (64x32), Raspberry Pi Wide (128x64) and Raspberry Pi Square (64x64) connected to matrix LED panels
*   Pixoticker (limited memory, not recommended)

Developing additional clients for Tronbyt Server is straightforward: pull WebP images from the `/next` endpoint and loop the animation for the duration specified in the `Tronbyt-Dwell-Secs` response header. Display brightness can optionally be set using the `Tronbyt-Brightness` header (0-100).

**Installation Methods:**
*   **Docker:** The recommended method uses Docker Compose with a configuration file. Video Tutorial : [Raspberry Pi Setup with Docker](https://youtu.be/UeHzD0uFxRo)
*   **Home Assistant:** https://github.com/kaffolder7/ha-app-tronbyt-server
*   **Bare metal (Homebrew):** Install using Homebrew:
    ```bash
    brew install tronbyt-server
    ```
*   **Bare metal (from source):** Build from source using Go.
*   **RaspberryPI Install Script** `curl -fsSL https://raw.githubusercontent.com/tronbyt/server-docker-compose/main/rpi_setup.sh | bash`

**Running the Application:**
*   For Docker installations, use `docker compose up -d`.
*   For Homebrew installations: `brew services start tronbyt-server`.
*   For native installations (from source): `go build -o tronbyt-server ./cmd/server && ./tronbyt-server`
*   Access the web app at `http://localhost:8000`.

### CLI Commands

The `tronbyt-server` binary supports additional commands for administration:

*   **`reset-password <username> <new_password>`**: Resets the password for a specified user.
    ```bash
    ./tronbyt-server reset-password admin newsecretpassword
    ```

*   **`health [url]`**: Performs a health check against the running server. Defaults to `http://localhost:8000/health`.
    ```bash
    ./tronbyt-server health
    ./tronbyt-server health https://your-tronbyt-server.com/health
    ```

*   **`update-system-apps`**: Updates the system apps repo.
    ```bash
    ./tronbyt-server update-system-apps
    ```

### Monitoring

*   **`/metrics`**: Exposes Prometheus-compatible metrics for monitoring the server's health and performance. This endpoint includes application-specific metrics (`tronbyt_*` for renders, device activity, HTTP requests, users/devices/apps counts), GORM database connection pool stats, and standard Go runtime metrics.
    ```bash
    curl http://localhost:8000/metrics
    ```
*   **`/debug/pprof/`**: Exposes Go pprof endpoints when `ENABLE_PPROF=true` is set. Disabled by default.

**Quick Start Guide:**
1.  Access the web app at `http://localhost:8000`.
2.  Add your Tronbyt as a device.
3.  Click "Firmware," enter WiFi credentials, and generate/download the firmware.
4.  Use the ESPHome firmware flasher to flash your Tidbyt into a Tronbyt.
5.  Add and configure an app via the built-in Pixlet interface.
6.  Save to see the app preview.

**Ports:** The web app is exposed on port `8000`.

### Updating

*   Docker containers: `docker compose pull && docker compose up -d`.

**Migration from v1.x:**
If you are upgrading from the Python version (v1.x) and using the default SQLite database:
1.  Ensure your old `usersdb.sqlite` file is in the data directory. If your Python installation had a separate `users` volume/directory (e.g., mounted at `/app/users` in Docker), this `users` directory needs to be attached as a volume to the Go server's main data directory (e.g., `/app/data/users` or directly at `/app/users`) during the first migration run. After successful migration, this old `users` volume can be safely detached and removed, as all its relevant contents will have been migrated to the new `data` structure.
2.  Start the new server.
3.  The server will automatically detect the legacy database and migrate your users, devices, and apps to the new `tronbyt.db` format.
4.  The legacy database will be renamed to `usersdb.sqlite.bak` after successful migration.

**Development:**
*   Clone the repository and use `docker compose -f docker-compose.dev.yaml up -d --build` for Docker development.
*   For native development:
    *   Run directly: `go run ./cmd/server`.
    *   With live-reloading (using [Air](https://github.com/air-verse/air)):
        Run: `PRODUCTION=false go tool air`

**Configuration:**

The server can be configured via environment variables or `.env` file:
*   `DB_DSN`: Database connection string (default: `data/tronbyt.db`). Supports SQLite, PostgreSQL, and MySQL.
*   `DATA_DIR`: Directory for data files (default: `data`).
*   `TRONBYT_HOST`: Listen address (default: empty / all interfaces).
*   `TRONBYT_PORT`: Listen port (default: `8000`).
*   `TRONBYT_UNIX_SOCKET`: Path to Unix socket to listen on (optional).
*   `TRONBYT_SSL_KEYFILE` & `TRONBYT_SSL_CERTFILE`: Paths to TLS key/cert for native HTTPS.
*   `TRONBYT_TRUSTED_PROXIES`: Trusted proxy CIDR ranges (default: `*`).
*   `ENABLE_PPROF`: Set to `1` to enable pprof routes at `/debug/pprof/` (default: `0`).
*   `ENABLE_USER_REGISTRATION`: Allow open user registration (default: `true`).
*   `ENABLE_UPDATE_CHECKS`: Check for new releases on startup (default: `true`).
*   `MAX_USERS`: Maximum number of user accounts (default: `0` / unlimited).
*   `SINGLE_USER_AUTO_LOGIN`: Skip login when only one user exists (default: `false`).
*   `SYSTEM_APPS_REPO`: Git repository URL for system apps (default: `https://github.com/tronbyt/apps.git`). End the URL with `#branch-name` to use a branch other than the default.
*   `SYSTEM_APPS_AUTO_REFRESH`: Initial default for refreshing the system apps repository every 12 hours (default: `false`). Administrators can change this under **Settings → Content and Firmware**; a saved web preference takes precedence on later starts.
*   `CUSTOM_APPS_AUTO_REFRESH`: Automatically refresh every user's custom apps repository, every 12h (default: `false`).
*   `NIBLET_CLOUD_URL`: Source of the install counts shown on system app cards, refreshed at most daily, and destination of the opt-in community app stats in System Settings, which are off by default (default: `https://cloud.heyniblet.com`). Set to an empty string to disable both. Administrators can also turn off all Niblet Cloud traffic under **Settings → Admin** without changing this variable.
*   `FIRMWARE_REPO`: GitHub repository URL for ESP32 firmware binaries (default: `https://github.com/tronbyt/firmware-esp32`). Set to `"none"` to disable ESP32 firmware fetching.
*   `TRONBERRY_REPO`: GitHub repository URL for Tronberry (Raspberry Pi) binaries (default: `https://github.com/tronbyt/tronberry`). Set to `"none"` to disable Tronberry binary fetching.
*   `SCREENY_STUDIO_URL`: Base URL of a running `screeny-studio` instance (e.g. `http://screeny-studio:8787`). When set, Screeny patches are automatically discovered and badged with `[Screeny]` in the Add App catalog, translated to native Pixlet configuration forms, and rendered into 30 FPS animations.
*   `SCREENY_FPS`: Frame rate for Screeny animations (default: `30`).
*   `SCREENY_MAX_WEBP_BYTES`: Default maximum WebP animation chunk size in bytes (default: `5242880` / 5 MB).
*   `SCREENY_MAX_WEBP_BYTES_ESP`: Maximum chunk size for ESP32 devices (default: `160000` / 160 KB) to fit safely within device RAM and firmware buffer limits during double-buffering (on Tidbyt Gen 1). Devices with expanded 460,000-byte firmware buffers (ESP32-S3, Matrix Wide, Tronbyt S3 Wide, Gen 2) automatically utilize 450 KB (`450000` bytes), and Pixoticker uses 50 KB (`50000` bytes).
*   `SCREENY_MAX_WEBP_BYTES_RPI`: Maximum chunk size for Raspberry Pi and Tronberry devices (default: `5242880` / 5 MB) utilizing their expanded memory capacity.
*   `SCREENY_SCALE_FILTER`: Global 128x64 upscaling filter algorithm: `bicubic`, `scale2x`, `bilinear`, `nearest` (default: `bicubic`). Can also be customized per installed app instance in the web UI.
*   `GITHUB_TOKEN`: GitHub token for private app repositories (optional).
*   `REDIS_URL`: Redis connection string for caching (optional).
*   `LOG_LEVEL`: Logging verbosity: `DEBUG`, `INFO`, `WARN`, `ERROR` (default: `INFO`).
*   `TRONBYT_BRANCH`: Branch name override for footer links (e.g. `feat/custom-branch`, optional).
*   `TRONBYT_REPO_URL`: Repository URL override for footer links and commit resolution (optional).
*   `TRONBYT_COMMIT`: Commit hash override for footer display (optional).

### External App Providers & Screeny 30 FPS Multi-Chunk Streaming

Tronbyt Server supports pluggable external app providers via the `ExternalAppProvider` interface. For a complete step-by-step setup guide using Docker, see [SCREENY_SETUP.md](SCREENY_SETUP.md).

When `SCREENY_STUDIO_URL` is configured:
1. **Catalog & Parameters:** Screeny patches (Clocks, Split-Flap, Boids, Bats, Leaves, Flyover, etc.) appear in the Add App catalog badged as `[Screeny]`. Patch options are translated into native schema configuration controls.
2. **30 FPS Native Frame Capture:** The server streams frames from `screeny-studio` via WebSocket at 30 fps, decoding wire UDP or preview packets with pure Go implementations of PAL5, PAL8_LZ, PAL4_LZ, BC1_DUAL, and SOLID codecs.
3. **High-Resolution 128x64 Scaling for Tronberry / Raspberry Pi:**
   - Screeny renders internally at 64x32. Rather than naive 2x nearest-neighbor pixel doubling (which produces chunky 4-LED blocks), Tronbyt Server provides advanced real-time integer scalers:
     - **Bicubic (Catmull-Rom with anti-ringing, default):** Calculates smooth continuous subpixel gradients across all 8,192 physical LEDs on 128x64 panels—ideal for 3D shaders, lighting gradients, and smooth particle animations.
     - **Scale2x / EPX (AdvMAME2x):** Pattern-matches diagonal pixel transitions to smooth angles without blur, keeping exact palette colors razor-sharp—ideal for clocks, split-flap airport boards, dials, and retro pixel art.
     - **Bilinear:** Smooth 4-way subpixel color interpolation.
     - **Nearest:** Classic retro 2x2 LED block doubling.
   - Users can select their preferred filter globally via `SCREENY_SCALE_FILTER` or override it per app instance in the Tronbyt web UI app settings (automatically offered for devices that support 2x scaling, such as Tronberry / Raspberry Pi 128x64).
4. **Multi-Chunk WebP Streaming:**
   - For high-capacity clients (Raspberry Pi / `tronberry`), animations up to 5 MB (configurable via `SCREENY_MAX_WEBP_BYTES_RPI`) stream in a single high-fidelity chunk.
   - For memory-constrained clients (ESP32 / Tidbyt), animations exceeding the device limit (160 KB on Tidbyt Gen 1, 450 KB on ESP32-S3 / Wide) or 5,000 ms duration are seamlessly split into sequential chunks with exact playback durations, preventing hardware buffer exhaustion and heap allocation errors.
   - **WebSocket (`/ws`):** Chunks stream sequentially using firmware double-buffering (chunk 0 displays while chunk 1 is pre-buffered in the hardware queue, swapping seamlessly on dwell completion) before rotating to the next app.
   - **HTTP (`/next`):** Sequential chunks are tracked per device and served with individual `Tronbyt-Dwell-Secs` headers before advancing rotation.
   - **Fault Tolerance:** If Screeny Studio is temporarily unreachable, Tronbyt Server falls back to cached chunk files to avoid interrupted playback.
5. **Catalog Provider Filter & Live Previews:**
   - **Dynamic Provider Filter:** When multiple providers (e.g. Tronbyt and Screeny) are active, a Provider dropdown filter dynamically appears on the Add App page, allowing users to filter by provider or view all apps together. If only one provider is configured, the dropdown automatically hides.
   - **Full-Duration Uninterrupted Previews:** Both in the web configuration UI and during device preview pushes, previews span the complete dwell time window without premature looping. The web UI renders and caches complete, unchunked WebP animations (`<appBasename>_full.webp`) matching the full dwell time, while device preview pushes stream all sequential chunks over WebSocket (or HTTP) to fill the device time window with an uninterrupted animation.
   - **Preview Generation & Crossfade Elimination:** Screeny patches generate on-demand animated WebP previews with automatic disk caching under `cache/previews/<provider>/`. Screeny Studio's built-in 2.0-second patch crossfade is automatically detected and skipped during frame capture, ensuring live catalog thumbnails and rendered device animations are 100% clean, crisp, and free from app-blending artifacts.

**HTTPS (TLS):**
*   Can be achieved by configuring `TRONBYT_SSL_KEYFILE` and `TRONBYT_SSL_CERTFILE`.
*   Or by fronting the service with a reverse proxy like Caddy (see `docker-compose.https.yaml`).
*   After enabling HTTPS, configure the health check to use the corresponding URL, e.g. `test: ["CMD", "/app/tronbyt-server", "health", "https://tronbyt.example.com/health"]`.

**Cache:** By default, an in-memory cache is used. Redis can be configured via `REDIS_URL` for persistent caching across container restarts.

