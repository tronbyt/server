# Setting Up Screeny Studio with Tronbyt Server (Docker & Web UI)

This guide walks you through setting up [Screeny Studio](https://github.com/gotwalt/screeny) using Docker and connecting it to your **Tronbyt Server** instance. 

With Screeny Studio connected, you can discover, configure, and stream beautiful **30 FPS generative animations, physics simulations, and procedural art** (such as Boids, Split-Flap Clock, Metaballs, Bats, Flyover, Leaves, and Shaders) directly to your Tidbyt Gen 1, Gen 2, MatrixPortal, and Tronberry (Raspberry Pi) devices.

---

## 1. Quick Overview

```mermaid
flowchart LR
    Screeny["Screeny Studio (Docker)<br/>Port 8787"] -->|WebSocket Stream (30 FPS)| Tronbyt["Tronbyt Server<br/>Port 8000"]
    Tronbyt -->|Multi-Chunk WebP| Hardware["Tidbyt / ESP32 / Tronberry<br/>64x32 or 128x64 Matrix"]
```

* **Screeny Studio** renders high-framerate procedural canvas graphics and exposes a REST API (`/api/v1/bootstrap`) and WebSocket preview stream on port `8787`.
* **Tronbyt Server** queries Screeny's catalog, translates patch options into standard Tronbyt configuration forms, captures frames at 30 FPS, applies integer upscaling for 128x64 displays, and partitions the animations into memory-safe chunks using Tronbyt's Multi-Chunk engine.
* **Resilience:** Tronbyt caches rendered animations on disk. If your Screeny container restarts or is temporarily offline, your devices keep running smoothly without interruption.

---

## 2. Setting Up Screeny Studio in Docker

The official container image `ghcr.io/gotwalt/screeny-studio:latest` provides Screeny Studio with GPU hardware acceleration (Vulkan) for rendering 30 FPS generative patches.

### Docker Compose Service

Add the `screeny` service to your `docker-compose.yml`:

```yaml
services:
  screeny:
    image: ghcr.io/gotwalt/screeny-studio:latest
    container_name: screeny-studio
    restart: unless-stopped
    network_mode: host
    devices:
      - /dev/dri:/dev/dri
    group_add:
      - "993"
    environment:
      - TZ=${TZ:-America/Denver}
      - WGPU_BACKEND=vulkan
      - SCREENY_STATE_DIR=/data
      - SCREENY_LISTEN=0.0.0.0:8787
      - SCREENY_PORT=8787
    volumes:
      - /data/docker/tronbyt/screeny:/data
    healthcheck:
      test: ["CMD-SHELL", "curl -fsS -o /dev/null http://127.0.0.1:8787/healthz"]
      interval: 30s
      timeout: 5s
      start_period: 30s
      retries: 3
    stop_grace_period: 20s
    logging:
      driver: json-file
      options:
        max-size: "10m"
        max-file: "3"
```

> [!TIP]
> * **Hardware Acceleration:** Mounting `/dev/dri:/dev/dri` and passing `group_add: ["993"]` (or your host's render/video group GID) grants Screeny direct access to GPU acceleration for smooth 30 FPS animations.
> * **Host Networking:** With `network_mode: host`, Screeny binds directly to host port `8787`.
>   * If Tronbyt Server is also running on the host (or in `network_mode: host`), set Studio URL to `http://127.0.0.1:8787`.
>   * If Tronbyt Server is running inside a Docker bridge container, set Studio URL to your host LAN IP: `http://192.168.1.X:8787` (or `http://host.docker.internal:8787`).
>   * Alternatively, if running both containers on the same Docker bridge network, remove `network_mode: host`, add `ports: ["8787:8787"]` to Screeny, and connect via `http://screeny-studio:8787`.

Start Screeny:
```bash
docker compose up -d screeny
```

---

## 3. Connecting Screeny to Tronbyt Server

You can connect Screeny using either the Tronbyt Web UI or environment variables.

### Method 1: Tronbyt Web UI (No Restart Needed)

1. Open your Tronbyt Server web dashboard (e.g. `http://localhost:8000`).
2. Navigate to **Settings** &rarr; **Content and Firmware**.
3. Scroll down to the **External App Providers** card:
   - **Studio URL:** 
     - If Tronbyt is running on the host: `http://127.0.0.1:8787`
     - If Tronbyt is running in a Docker bridge container: `http://192.168.1.X:8787` (your host LAN IP)
     - If both share a Docker bridge network: `http://screeny-studio:8787`
   - **128x64 Scaling Filter:** Choose your preferred default upscaler (`Bicubic`, `Scale2x / EPX`, `Bilinear`, `Nearest`).
4. Click **Save & Connect**.
5. The status badge will instantly turn green and display:
   ```text
   Connected (15 patches available)
   ```

### Method 2: Environment Variables (`.env`)

Add the following to your `.env` file or container environment:

```ini
# Screeny Studio URL (adjust for host LAN IP or shared container name)
SCREENY_STUDIO_URL="http://192.168.1.X:8787"

# Frame rate for animations (default: 30)
SCREENY_FPS=30

# Default upscaling filter for 128x64 displays (e.g. Tronberry / Raspberry Pi)
# Options: bicubic, scale2x, bilinear, nearest
SCREENY_SCALE_FILTER="bicubic"
```

Restart Tronbyt Server to load the environment variables.

---

## 4. Installing and Customizing Screeny Apps

Once connected, Screeny apps appear right inside your Tronbyt app catalog:

1. In Tronbyt, navigate to **Add App**.
2. If multiple providers are active, a **Provider** filter appears at the top. Select **Screeny** (or leave on *All Providers*).
3. Screeny apps are clearly badged with `[Screeny]`. Examples include:
   * **Boids:** Swarm intelligence flocking simulation.
   * **Split-Flap:** Classic mechanical airport / train station board.
   * **Metaballs:** Smooth organic fluid physics.
   * **Flyover:** Retro 3D terrain wireframe flight.
   * **Leaves / Bats:** Atmospheric seasonal particle simulations.
4. Click any patch to open its configuration page.
   * Parameter sliders, color pickers, and toggles from Screeny Studio are automatically mapped into native Tronbyt inputs.
5. If your device is a 128x64 display (such as Tronberry or Raspberry Pi), you can select an app-specific **Scaling Filter**:
   * **Bicubic (Default):** Smooth Catmull-Rom subpixel gradients—stunning for shaders, metaballs, and light particles.
   * **Scale2x / EPX:** Ultra-sharp pixel art and clean typography without blur—ideal for clocks and split-flap boards.
   * **Nearest:** Retro 2&times;2 pixel doubling.
6. Click **Save App**. Your device will now render and display the 30 FPS animation!

---

## 5. How It Works Under the Hood

* **Crossfade Cleanup:** Screeny Studio includes a built-in 2.0-second crossfade transition between patches. Tronbyt Server automatically detects and discards these transition frames, ensuring all rendered device animations and catalog previews are 100% crisp, clean, and free of ghosting.
* **Multi-Chunk Memory Protection:** High-framerate 30 FPS WebP animations can reach 1–2 MB. While Raspberry Pi can handle this in a single file, microcontrollers like ESP32 (Tidbyt Gen 1 & Gen 2) have strict image buffer limits. Tronbyt Server's multi-chunk engine automatically splits animations into sequential chunks matching the device's exact memory budget and feeds them seamlessly via double-buffering over WebSocket or HTTP.
* **Offline Resilience:** Once an animation is rendered, its chunks are cached in `data/webp/<device_id>/`. If your Screeny container is stopped, restarted, or updating, your devices continue cycling their cached animations without any blank screens.

---

## 6. Troubleshooting & FAQ

### Q: The status badge says "Error connecting: Get http://localhost:8787/api/v1/bootstrap: dial tcp: connect: connection refused"
**A:** If Tronbyt Server is running inside a Docker container, `localhost` refers to the *Tronbyt container itself*, not your host computer.
* If using Docker Compose: Use `http://screeny-studio:8787` (the service name).
* If running Tronbyt in Docker and Screeny on host: Use `http://host.docker.internal:8787` (Docker for Mac/Windows) or `http://172.17.0.1:8787` / your host's LAN IP `http://192.168.1.X:8787` (Linux).

### Q: Can I verify Screeny Studio directly with curl?
**A:** Yes! Run:
```bash
curl -s http://localhost:8787/api/v1/bootstrap | jq .
```
You should see a JSON payload listing available patches, versions, and panel settings.

### Q: Can I run Screeny without Docker?
**A:** Absolutely. If you have Rust installed:
```bash
git clone https://github.com/gotwalt/screeny.git
cd screeny
cargo run --release -p screeny-studio
```
Then point Tronbyt Server to `http://<machine-ip>:8787`.
