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

### Option A: `docker-compose.yml` (Recommended)

If you use Docker Compose to run Tronbyt Server, you can run `screeny-studio` alongside it on the same Docker bridge network.

#### 1. Create a `Dockerfile.screeny`
In your Tronbyt Server directory (or adjacent folder), create a `Dockerfile.screeny`:

```dockerfile
FROM rust:1.80-slim-bookworm AS builder

WORKDIR /usr/src/screeny
RUN apt-get update && apt-get install -y --no-install-recommends \
    git \
    pkg-config \
    libssl-dev \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

RUN git clone https://github.com/gotwalt/screeny.git .
RUN cargo build --release -p screeny-studio

FROM debian:bookworm-slim

WORKDIR /app
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /usr/src/screeny/target/release/screeny-studio /usr/local/bin/screeny-studio

EXPOSE 8787

# Screeny Studio listens on port 8787
CMD ["screeny-studio"]
```

#### 2. Add Screeny to your `docker-compose.yml`

```yaml
version: '3.8'

services:
  tronbyt:
    image: ghcr.io/tronbyt/server:latest
    container_name: tronbyt-server
    ports:
      - "8000:8000"
    environment:
      # Connect Tronbyt to Screeny using the Docker service name
      - SCREENY_STUDIO_URL=http://screeny-studio:8787
      - SCREENY_FPS=30
      - SCREENY_SCALE_FILTER=bicubic
    volumes:
      - ./data:/app/data
    restart: unless-stopped
    depends_on:
      - screeny-studio

  screeny-studio:
    build:
      context: .
      dockerfile: Dockerfile.screeny
    container_name: screeny-studio
    ports:
      - "8787:8787"
    restart: unless-stopped
```

#### 3. Start the containers
```bash
docker compose up -d --build
```

---

### Option B: Standalone `docker run`

If you prefer to run Screeny Studio as an independent container:

1. **Build the container:**
   ```bash
   docker build -t screeny-studio -f Dockerfile.screeny .
   ```

2. **Run the container:**
   ```bash
   docker run -d \
     --name screeny-studio \
     -p 8787:8787 \
     --restart unless-stopped \
     screeny-studio
   ```

3. **Verify it is running:**
   Open `http://localhost:8787` (or `http://<your-server-ip>:8787`) in your web browser. You should see the Screeny Studio web interface.

---

## 3. Connecting Screeny to Tronbyt Server

You can connect Screeny using either the Tronbyt Web UI or environment variables.

### Method 1: Tronbyt Web UI (No Restart Needed)

1. Open your Tronbyt Server web dashboard (e.g. `http://localhost:8000`).
2. Navigate to **Settings** &rarr; **Content and Firmware**.
3. Scroll down to the **External App Providers** card:
   - **Studio URL:** 
     - If both containers are in Docker Compose: `http://screeny-studio:8787`
     - If running on your host machine or LAN: `http://192.168.1.X:8787` (replace with your host's LAN IP; do **not** use `localhost` from inside a container).
   - **128x64 Scaling Filter:** Choose your preferred default upscaler (`Bicubic`, `Scale2x / EPX`, `Bilinear`, `Nearest`).
4. Click **Save & Connect**.
5. The status badge will instantly turn green and display:
   ```text
   Connected (15 apps discovered)
   ```

### Method 2: Environment Variables (`.env`)

Add the following to your `.env` file or container environment:

```ini
# Screeny Studio URL
SCREENY_STUDIO_URL="http://screeny-studio:8787"

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
