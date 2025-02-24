# Tronbyt Server

This is a Flask app for managing your apps on your Tronbyt (flashed Tidbyt). This project is designed to run your Tronbyt/Tidbyt completely locally without relying on the backend servers operated by Tidbyt.

## Why use this?

Compared to the stock firmware, Tronbyt provides the following advantages:

- A web UI with much better discoverability. You'd be surprised how many community apps could never be found in the Tidbyt app.
- Everything runs locally without a cloud dependency. If Tidbyt's servers go offline, your Tronbyt will keep working indefinitely.
- Fresher community apps (because the Tidbyt community repository is stale).
- When running locally, some APIs start working again whereas Tidbyt's servers were blocked (for example the Surfline apps).
- Community support.

Of course, there are also some drawbacks:

- There is no mobile app yet (that would be a nice project for you!).
- Notifications have a slightly higher latency (because the device waits for the next poll instead of getting a push via MQTT).
- Some built-in apps are not available (a shrinking number, thanks to community members who recreate them).
- Tools which use `pixlet push` to push images to a Tidbyt are incompatible (they need to be changed to accept different URLs).

Some community projects like [TidbytAssistant](https://github.com/savdagod/TidbytAssistant) have already been updated to work with Tronbyt.

## Getting Started

If you've been invited to use my public instance of this server login there and skip to the quickstart guide.

### Prerequisites

- Docker
- Docker Compose (optional, but recommended)

### Installation

It is possible to just start the server on the default ports in the background with a one-liner:

```sh
docker run -d -e SERVER_HOSTNAME=<YOUR_SETTING_HERE> -e SERVER_PORT=8000 -e PIXLET_RENDER_PORT1=5100 -e PRODUCTION=1 -p 8000:8000 -p 5100:5100 -p 5101:5101 ghcr.io/tronbyt/server
```

That said, the recommended installation method uses Docker Compose with a configuration file for your settings:

1. Download the [Compose project](https://raw.githubusercontent.com/Tronbyt/server/refs/heads/master/docker-compose.yaml) (or use the contents to add a service to an existing project).

2. Copy the [example environment file](https://raw.githubusercontent.com/Tronbyt/server/refs/heads/master/.env.example) and modify it as needed:

   ```sh
   curl https://raw.githubusercontent.com/Tronbyt/server/refs/heads/master/.env.example > .env
   ```

3. Set the `SERVER_HOSTNAME_OR_IP` value in the `.env` file. IP addresses will work too.

### Running the Application

1. Build and start the Docker containers:

   ```sh
   docker compose up -d
   ```

2. Access the web app at [http://localhost:8000](http://localhost:8000) (or your configured domain or IP) with the default login credentials:
   - Username: `admin`
   - Password: `password`

### Quick Start Guide

1. Access the web app at [http://localhost:8000](http://localhost:8000) (or your configured domain) with the default login credentials: `admin/password`.
2. Add your Tronbyt as a device in the manager. The default installation will already have a device named "Tronbyt 1".
3. Click on the "Firmware" button and enter your WiFi credentials. The image URL should be prefilled.
4. Click "Generate Firmware" and download your firmware file.
5. Download the ESPHome firmware flasher from [this link](https://github.com/esphome/esphome-flasher/releases) and use it to flash your Tidbyt into a Tronbyt.
6. Add an app and configure it via the built-in Pixlet interface.
7. Click "Save" and you'll see the app preview on the app listing page.

### Ports

- The web app is exposed on port `8000`.
- Additional ports `5100` and `5101` are also exposed.

### Default Login

- Username: `admin`
- Password: `password`

### Notes

- Ensure that the `SERVER_HOSTNAME_OR_IP` value is set in the `.env` file if you are not running the application locally. An IP address will also work here.

### Development

1. Clone the repository:

   ```sh
   git clone https://github.com/Tronbyt/server.git
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
timeout = 120
worker_tmp_dir = "/dev/shm"
preload_app = False
reload = False
keyfile = "/ssl/privkey.pem"
certfile = "/ssl/fullchain.pem"
ssl_version = "TLSv1_2"

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
