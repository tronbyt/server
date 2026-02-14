# Tronbyt Server

The Tronbyt Server is a Go-based application designed to manage apps on Tronbyt devices locally, without relying on Tidbyt's backend servers. It offers a web UI for app discoverability and operates independently of cloud dependencies, ensuring continued functionality even if Tidbyt's servers are offline. The server also enables some APIs that were previously blocked by Tidbyt's servers, such as Surfline apps, and supports custom hardware.

However, there are some drawbacks, including the lack of a mobile app, slightly higher latency for notifications, and limited support for some built-in apps and apps relying on Tidbyt's cloud services for secrets.

**Prerequisites for running Tronbyt Server:**
*   **Docker** and **Docker Compose** (optional but recommended) for containerized deployment.
*   **Homebrew** for macOS and Linux bare-metal installations (provides pre-built binary).
*   **Go 1.25+** if building from source.

**Supported Devices:**
*   Tidbyt Gen1 and Gen2
*   Tronbyt S3
*   Raspberry Pi connected to a 64x32 matrix LED
*   Pixoticker (limited memory, not recommended)

Developing additional clients for Tronbyt Server is straightforward: pull WebP images from the `/next` endpoint and loop the animation for the duration specified in the `Tronbyt-Dwell-Secs` response header. Display brightness can optionally be set using the `Tronbyt-Brightness` header (0-100).

**Installation Methods:**
*   **Docker:** The recommended method uses Docker Compose with a configuration file. Video Tutorial : [Raspberry Pi Setup with Docker](https://youtu.be/UeHzD0uFxRo)
*   **Bare metal (Homebrew):** Install using Homebrew:
    ```bash
    brew install tronbyt-server
    ```
*   **Bare metal (from source):** Build from source using Go.

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

### Monitoring

*   **`/metrics`**: Exposes Prometheus-compatible metrics for monitoring the server's health and performance. This endpoint includes standard Go runtime metrics and process-level metrics.
    ```bash
    curl http://localhost:8000/metrics
    ```

**Quick Start Guide:**
1.  Access the web app at `http://localhost:8000`.
2.  Add your Tronbyt as a device.
3.  Click "Firmware," enter WiFi credentials, and generate/download the firmware.
4.  Use the ESPHome firmware flasher to flash your Tidbyt into a Tronbyt.
5.  Add and configure an app via the built-in Pixlet interface.
6.  Save to see the app preview.

**Ports:** The web app is exposed on port `8000`.

**User Registration:** By default, only the admin can create new user accounts. Open user registration can be enabled by setting `ENABLE_USER_REGISTRATION=1` in the `.env` file.

**Updating:**
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
        Run: `PRODUCTION=0 go tool air`

**Configuration:**

The server can be configured via environment variables or `.env` file:
*   `TRONBYT_HOST`: Listen address (default: empty / all interfaces).
*   `TRONBYT_PORT`: Listen port (default: `8000`).
*   `TRONBYT_UNIX_SOCKET`: Path to Unix socket to listen on (optional).
*   `TRONBYT_SSL_KEYFILE` & `TRONBYT_SSL_CERTFILE`: Paths to TLS key/cert for native HTTPS.
*   `SYSTEM_APPS_REPO`: Git repository URL for system apps.
*   `REDIS_URL`: Redis connection string for caching (optional).

**HTTPS (TLS):**
*   Can be achieved by configuring `TRONBYT_SSL_KEYFILE` and `TRONBYT_SSL_CERTFILE`.
*   Or by fronting the service with a reverse proxy like Caddy (see `docker-compose.https.yaml`).
*   After enabling HTTPS, configure the health check to use the corresponding URL, e.g. `test: ["CMD", "/app/tronbyt-server", "health", "https://tronbyt.example.com/health"]`.

**Cache:** By default, an in-memory cache is used. Redis can be configured via `REDIS_URL` for persistent caching across container restarts.
