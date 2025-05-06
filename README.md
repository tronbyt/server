# Tronbyt Server

This is a Flask app for managing your apps on your Tronbyt (flashed Tidbyt). This project is designed to run your Tronbyt/Tidbyt completely locally without relying on the backend servers operated by Tidbyt.

## Why use this?

Compared to the original Tidbyt, Tronbyt provides the following advantages:

- A web UI with much better discoverability. You'd be surprised how many community apps could never be found in the Tidbyt app.
- Everything runs locally without a cloud dependency. If Tidbyt's servers go offline, your Tronbyt will keep working indefinitely.
- When running locally, some APIs start working again whereas Tidbyt's servers were blocked (for example the Surfline apps).
- Community support.
- Support for custom hardware: you can point your own firmware to a Tronbyt server to pull images for display on any 64x32 matrix LED display.

Of course, there are also some drawbacks:

- There is no mobile app yet (that would be a nice project for you!).
- Notifications have a slightly higher latency (because the device waits for the next poll instead of getting a push via MQTT).
- Some built-in apps are not available (a shrinking number, thanks to community members who recreate them).
- Apps which rely on Tidbyt's cloud services for storing secrets (such as OAuth credentials) are not yet supported.
- Tools which use `pixlet push` to push images to a device are incompatible unless they have been updated to accept alternative URLs.

Some community projects like [TidbytAssistant](https://github.com/savdagod/TidbytAssistant) have already been updated to work with Tronbyt.

## Getting Started

If you've been invited to use my public instance of this server login there and skip to the quickstart guide.

### Prerequisites

If you want to run Tronbyt Server as a container, you need

- [Docker](https://www.docker.com)
- [Docker Compose](https://docs.docker.com/compose/install/) (optional, but recommended)

Otherwise, you need

- [Homebrew](https://brew.sh)

### Supported Devices

- [Tidbyt Gen1](https://tidbyt.com/collections/tidbyt-smart-displays)
- [Tidbyt Gen2](https://tidbyt.com/collections/tidbyt-smart-displays)
- [Pixoticker](https://www.etsy.com/listing/1801658771/pixoticker-live-stock-crypto-forex)
- [Raspberry Pi](https://github.com/tronbyt/tronberry) connected to a 64x32 matrix LED

It's rather easy to develop additional clients for Tronbyt Server: just pull images in WebP format from the `/next` endpoint and loop the animation for the number of seconds specified in the `Tronbyt-Dwell-Secs` response header. Optionally set the display brightness to the value in the `Tronbyt-Brightness` header (0 - 100).

### Installation

#### Docker

It is possible to just start the server on the default ports in the background with a one-liner:

```sh
docker run -d -e SERVER_HOSTNAME=<YOUR_SETTING_HERE> -e SERVER_PORT=8000 -e PRODUCTION=1 -p 8000:8000 ghcr.io/tronbyt/server
```

That said, the recommended installation method uses Docker Compose with a configuration file for your settings:

1. Download the [Compose project](https://raw.githubusercontent.com/tronbyt/server/refs/heads/main/docker-compose.yaml) (or use the contents to add a service to an existing project).

2. Copy the [example environment file](https://raw.githubusercontent.com/tronbyt/server/refs/heads/main/.env.example) and modify it as needed:

   ```sh
   curl https://raw.githubusercontent.com/tronbyt/server/refs/heads/main/.env.example > .env
   ```

3. Set the `SERVER_HOSTNAME_OR_IP` value in the `.env` file. IP addresses will work too.

#### Bare metal

1. Install tronbyt-server using [Homebrew](https://brew.sh) (available on macOS and Linux):

   ```sh
   brew install tronbyt/tronbyt/tronbyt-server
   ```

2. Copy the [example environment file](https://raw.githubusercontent.com/tronbyt/server/refs/heads/main/.env.example) and modify it as needed:

   ```sh
   curl https://raw.githubusercontent.com/tronbyt/server/refs/heads/main/.env.example > $(brew --prefix)/tronbyt-server/.env
   ```

### Running the Application

1. If you installed the application using Docker, build and start the containers:

   ```sh
   docker compose up -d
   ```

   If you followed the native route, start the service using

   ```
   brew services start tronbyt-server
   ```

2. Access the web app at [http://localhost:8000](http://localhost:8000) (or your configured domain or IP) with the default login credentials:
   - Username: `admin`
   - Password: `password`

### Quick Start Guide

1. Access the web app at [http://localhost:8000](http://localhost:8000) (or your configured domain) with the default login credentials: `admin/password`.
2. Add your Tronbyt as a device in the manager.
3. Click on the "Firmware" button and enter your WiFi credentials. The image URL should be prefilled.
4. Click "Generate Firmware" and download your firmware file.
5. Download the ESPHome firmware flasher from [this link](https://github.com/esphome/esphome-flasher/releases) and use it to flash your Tidbyt into a Tronbyt.
6. Add an app and configure it via the built-in Pixlet interface.
7. Click "Save" and you'll see the app preview on the app listing page.

### Ports

- The web app is exposed on port `8000`.

### Default Login

- Username: `admin`
- Password: `password`

### Notes

- Ensure that the `SERVER_HOSTNAME_OR_IP` value is set in the `.env` file if you are not running the application locally. An IP address will also work here.
- To update your install to the latest version simply run `docker compose pull && docker compose up -d`

### Updating from Earlier Versions

If you are upgrading from an earlier version of Tronbyt Server (earlier than version 1.0.0) that was running as `root`, you can switch to a non-root user for improved security. Follow these steps:

1. Stop your server:

   ```sh
   docker compose down
   ```

2. Adjust the permissions of the existing data files:

   ```sh
   docker compose run --rm --user=0 --entrypoint="" web chown -R tronbyt:tronbyt -f /app/system-apps.json /app/system-apps /app/tronbyt_server/static/apps /app/tronbyt_server/webp /app/users
   ```

3. Update your Compose file to include the following line under the service definition:

   ```yaml
   user: "tronbyt:tronbyt"
   ```

4. Restart the server:

   ```sh
   docker compose up -d
   ```

### Development

1. Clone the repository:

   ```sh
   git clone https://github.com/tronbyt/server.git
   cd server
   ```

2. Build and run the image using the local copy:

    ```sh
    docker compose -f docker-compose.dev.yaml up -d --build
    ```

### HTTPS (TLS)

If you'd like to serve tronbyt-server over HTTPS, you can do so by configuring Gunicorn or by fronting the service with a reverse proxy. The reverse proxy approach is more flexible and allows for automatic certificate provisioning and renewal. If you already have a certificate, you can also use that directly and avoid the sidecar container.

#### Reverse Proxy

The `docker-compose.https.yaml` file contains an example using [Caddy](https://caddyserver.com) as a lightweight reverse proxy which provides TLS termination. The default configuration uses [Local HTTPS](https://caddyserver.com/docs/automatic-https#local-https), for which Caddy generates its own certificate authority (CA) and uses it to sign certificates. The certificates are stored in the `certs` directory at `pki/authorities/local`.

If you want to make tronbyt-server accessible using a public DNS name, adjust `Caddyfile` to match your domain name and use one of the supporte [ACME challenges](https://caddyserver.com/docs/automatic-https#acme-challenges) (HTTP, TLS-ALPN, or DNS).

#### Gunicorn

The following example assumes that your private key and certificate are located next to your Compose file.

1. Create a file named `gunicorn.conf.py` in the same directory which looks like this:

```python
bind = "0.0.0.0:8000"
loglevel = "info"
accesslog = "-"
access_log_format = "%(h)s %(l)s %(u)s %(t)s %(r)s %(s)s %(b)s %(f)s %(a)s"
errorlog = "-"
workers = 4
threads = 4
timeout = 120
worker_tmp_dir = "/dev/shm"
preload_app = True
reload = False
keyfile = "/ssl/privkey.pem"
certfile = "/ssl/fullchain.pem"

def ssl_context(conf, default_ssl_context_factory):
    import ssl
    context = default_ssl_context_factory()
    context.minimum_version = ssl.TLSVersion.TLSv1_2
    return context
```

2. Make the files in PEM format and the configuration file available to the container:

```
    volumes:
      - ./gunicorn.conf.py:/app/gunicorn.conf.py
      - ./fullchain.cer:/ssl/fullchain.pem
      - ./privkey.pem:/ssl/privkey.pem
```

3. Restart the container.

Your Tronbyt server is now serving HTTPS.

See https://docs.gunicorn.org/en/latest/settings.html#settings for an exhaustive list of settings for Gunicorn.

### Cache

By default, tronbyt-server uses an in-memory cache for HTTP requests made by applets. This requires no setup,
but also means that the cache will start empty every time you start the container. To persist the cache across
containe restarts, configure Redis as in the [docker-compose.redis.yaml](docker-compose.redis.yaml) example.
