# Changelog: Tronbyt Server v2.0.0 (Go Rewrite)

We are excited to announce **Tronbyt Server v2.0.0**, a complete rewrite of the server from Python to **Go**! This release brings significant performance improvements, a simplified deployment process, and a more robust architecture, while maintaining compatibility with your existing data.

## 🚀 Major Changes

### Python to Go Rewrite
The entire backend has been rewritten in Go (1.25+). This change offers:
*   **Single Binary:** No more Python environment management. The server is now a single, static binary.
*   **Massive Efficiency:**
    *   **Image Size:** The container image is roughly **85% smaller**.
    *   **Memory Footprint:** Memory usage has been reduced by **~90%**.
*   **Performance:** Faster response times and lower latency for app rendering.
*   **Native Pixlet Integration:** Pixlet rendering is now handled natively within the Go application, improving stability and efficiency.
*   **Improved Reliability:** Pushing images to devices is now significantly more reliable, with more robust connection management and better error handling.

### Database Overhaul
*   **Relational Schema:** We've moved from a JSON-blob SQLite database to a structured relational schema using **GORM**.
*   **Support for External DBs:** While SQLite remains the default, the new architecture supports PostgreSQL and MySQL for larger deployments (configurable via drivers).
*   **Automatic Migration:** The server automatically detects your legacy `usersdb.sqlite`, migrates all users, devices, and apps to the new format, and creates a backup of your old data.

## ✨ New Features

*   **Responsive Real-time UI:** The web interface is now more responsive and updates in real-time via WebSockets, instantly reflecting changes made via the API.
*   **Native HTTPS:** Support for providing SSL key/cert files directly to the server, alongside the existing reverse-proxy patterns.
*   **HTTP/3 (QUIC) Support:** Native support for HTTP/3 when TLS certificates are provided, offering faster and more reliable connections over modern networks.
*   **Unix Domain Sockets:** Support for listening on Unix sockets (`TRONBYT_UNIX_SOCKET`), ideal for local communication and high-performance reverse proxy setups.
*   **Prometheus Metrics:** A new `/metrics` endpoint exposes Go runtime and application metrics for monitoring.
*   **CLI Tools:** The binary includes built-in commands for administration:
    *   `reset-password`: Manually reset a user's password.
    *   `health`: Perform health checks against the running server.
*   **Passkey Authentication:** Added support for passkey authentication (requires HTTPS on some browsers) for more secure and convenient logins.
*   **Over-The-Air (OTA) Updates:** Devices running compatible firmware can now be updated directly from the web interface. Updates are delivered via WebSocket commands or HTTP headers, streamlining the firmware management process.
*   **App Configuration Export/Import:** Users can now export app configurations to a JSON file and import them back into existing app installations. This makes it easy to backup configurations or replicate complex setups across different apps.
*   **ZIP-packaged App Support:** Users can now upload and run apps packaged as ZIP files. This enables more complex apps that split logic across multiple files, reference external assets like images, and include metadata via manifest files.

## 🛠 Deployment & DevOps

*   **Docker Images:** New, ultra-minimal Docker images.
*   **Docker Compose:** Updated `docker-compose.yaml` with variants for HTTPS, PostgreSQL, and Redis setups.
*   **Development:** Added `air` support for live-reloading during development.

## ⚠️ Breaking Changes & Migration

*   **Configuration:** Environment variables have been updated. Please check the `README.md` for the new configuration options.
*   **Volume Mapping:** If you previously mounted a separate volume at `/app/users`, you must continue to mount it at `/app/users` for the one-time migration to find your old database. After a successful migration, this volume mount can be removed.
*   **Ports:** The default port remains `8000`.

## 📝 How to Upgrade

1.  **Backup:** Always backup your `usersdb.sqlite` and `custom_apps` folder before upgrading.
2.  **Pull:** Update your Docker image to `v2.0.0` (or `latest`).
3.  **Run:** Start the container. The server will log the migration process:
    ```text
    INFO: Migrating database
    INFO: Migrated user username=admin
    INFO: Migration complete
    ```
4.  **Verify:** Log in to the web UI and verify your devices and apps are present.
