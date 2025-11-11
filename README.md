# Tronbyt Server

This is a FastAPI app for managing your apps on your Tronbyt (flashed Tidbyt). This project is designed to run your Tronbyt/Tidbyt completely locally without relying on the backend servers operated by Tidbyt.

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

## Getting Started

You can run Tronbyt-server on the free tier at Render.com.  Here is a [video walkthrough](https://youtu.be/2WfVaeX7BTk). 
MAKE SURE TO BACKUP YOUR DEVICE CONFIG JSON BEFORE REDEPLOYMENT.  Unlike normal docker Render.com will wipe everything on re-deploy. To help keep your instance from spinning down due to inactivity you can setup a free cron-job at cron-job.org with your currentapp url (eg. https://tronbyt-server.onrender.com/316a24fb/currentapp).  Set it to run once every 1 - 15 minutes and it should keep the server from being terminated.

### Prerequisites

If you want to run Tronbyt Server as a container, you need

- [Docker](https://www.docker.com)
- [Docker Compose](https://docs.docker.com/compose/install/) (optional, but recommended)
- [Video Walkthrough on Raspberry PI](https://youtu.be/ZyE1IC97dVw)

Otherwise, you need

- [Homebrew](https://brew.sh)

### Supported Devices

- [Tidbyt Gen1](https://tidbyt.com/collections/tidbyt-smart-displays)
- [Tidbyt Gen2](https://tidbyt.com/collections/tidbyt-smart-displays)
- [Tronbyt S3](https://pixohardware.store/product/tronbyt-dev-kit/)
- [Raspberry Pi](https://github.com/tronbyt/tronberry) connected to a 64x32 matrix LED
- [Pixoticker](https://www.etsy.com/listing/1801658771/pixoticker-live-stock-crypto-forex) (Limited memory, not recommended)

It's rather easy to develop additional clients for Tronbyt Server: just pull images in WebP format from the `/next` endpoint and loop the animation for the number of seconds specified in the `Tronbyt-Dwell-Secs` response header. Optionally set the display brightness to the value in the `Tronbyt-Brightness` header (0 - 100).

### Installation

#### Docker

It is possible to just start the server on the default ports in the background with a one-liner:

```sh
docker run -d -e PRODUCTION=1 -p 8000:8000 ghcr.io/tronbyt/server:1
```

That said, the recommended installation method uses Docker Compose with a configuration file for your settings:

1. Download the [Compose project](https://raw.githubusercontent.com/tronbyt/server/refs/heads/main/docker-compose.yaml) (or use the contents to add a service to an existing project).

2. Copy the [example environment file](https://raw.githubusercontent.com/tronbyt/server/refs/heads/main/.env.example) and modify it as needed:

   ```sh
   curl https://raw.githubusercontent.com/tronbyt/server/refs/heads/main/.env.example > .env
   ```

#### Bare metal

1. Install tronbyt-server using [Homebrew](https://brew.sh) (available on macOS and Linux):

   ```sh
   brew install tronbyt/tronbyt/tronbyt-server
   ```

   The command above installs the most recent release. If you like to live at the bleeding edge, you can also install the most recent code:

   ```sh
   brew install --HEAD tronbyt/tronbyt/tronbyt-server
   ```

2. Copy the [example environment file](https://raw.githubusercontent.com/tronbyt/server/refs/heads/main/.env.example) and modify it as needed:

   ```sh
   curl https://raw.githubusercontent.com/tronbyt/server/refs/heads/main/.env.example > $(brew --prefix)/var/tronbyt-server/.env
   ```

   You can further customize the server by editing the [Uvicorn settings](https://uvicorn.dev/settings/) (listening address, TLS certificate, etc.) in `$(brew --prefix)/var/tronbyt-server/.env`.

#### Unraid

1. Install via Unraid App Store by searching for `tronbyt-server`

2. Configure the environment variables as needed. For available options, refer to the [.env.example file](https://github.com/tronbyt/server/blob/main/.env.example).

3. Start the container.

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
For a more detailed walkthrough click here : [Walkthrough in google docs by JeffLac](https://docs.google.com/document/d/e/2PACX-1vS-TRGPnYvjnwR37GXy6GFsvGdhBeDC6iypncZ5jFNaT9mxcczneGqmRtdM98UVUi7Cl-KDl4XYedkZ/pub)
1. Access the web app at [http://localhost:8000](http://localhost:8000) (or your configured domain) with the default login credentials: `admin/password`.
2. Add your Tronbyt as a device in the manager.
3. Click on the "Firmware" button and enter your WiFi credentials. The image URL should be prefilled.
4. Click "Generate Firmware" and download your firmware file.
5. Download the ESPHome firmware flasher from [this link](https://github.com/Jason2866/ESP_Flasher/releases) and use it to flash your Tidbyt into a Tronbyt.
6. Add an app and configure it via the built-in Pixlet interface.
7. Click "Save" and you'll see the app preview on the app listing page.

### Ports

- The web app is exposed on port `8000`.

### Default Login

- Username: `admin`
- Password: `password`

### User Registration

By default, only the admin user can create new user accounts. If you want to allow open user registration (where anyone can create an account), set the `ENABLE_USER_REGISTRATION` environment variable to `1` in your `.env` file:

```
ENABLE_USER_REGISTRATION=1
```

When enabled, a "Create User" link will appear on the login page and in the navigation for unauthenticated users. The `MAX_USERS` setting still applies even with open registration enabled.

### Notes

- To update your containers to the latest version simply run `docker compose pull && docker compose up -d`.
- To update your native installation, run `brew upgrade tronbyt-server` and `brew services restart tronbyt-server`.

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

    or run the application natively:

    ```sh
    pdm install
    pdm run start
    ```

### HTTPS (TLS)

If you'd like to serve Tronbyt Server over HTTPS, you can do so by configuring Uvicorn or by fronting the service with a reverse proxy. The reverse proxy approach is more flexible and allows for automatic certificate provisioning and renewal. If you already have a certificate, you can also use that directly and avoid the sidecar container.

#### Reverse Proxy

The `docker-compose.https.yaml` file contains an example using [Caddy](https://caddyserver.com) as a lightweight reverse proxy which provides TLS termination. The default configuration uses [Local HTTPS](https://caddyserver.com/docs/automatic-https#local-https), for which Caddy generates its own certificate authority (CA) and uses it to sign certificates. The certificates are stored in the `certs` directory at `pki/authorities/local`.

If you want to make Tronbyt Server accessible using a public DNS name, adjust `Caddyfile` to match your domain name and use one of the supporte [ACME challenges](https://caddyserver.com/docs/automatic-https#acme-challenges) (HTTP, TLS-ALPN, or DNS).

#### Uvicorn

The following example assumes that your private key and certificate are located next to your Compose file.

1. Configure the certificate and private key in the compose file:

```
    environment:
      - UVICORN_SSL_KEYFILE=/ssl/privkey.pem
      - UVICORN_SSL_CERTFILE=/ssl/fullchain.pem
```

2. Make the files in PEM format and the configuration file available to the container:

```
    volumes:
      - ./fullchain.cer:/ssl/fullchain.pem
      - ./privkey.pem:/ssl/privkey.pem
```

3. Restart the container.

Your Tronbyt server is now serving HTTPS.

See https://uvicorn.dev/settings for an exhaustive list of settings for Uvicorn.

### Cache

By default, Tronbyt Server uses an in-memory cache for HTTP requests made by applets. This requires no setup,
but also means that the cache will start empty every time you start the container. To persist the cache across
containe restarts, configure Redis as in the [docker-compose.redis.yaml](docker-compose.redis.yaml) example.

## Development

### Creating a new release

1. Create a tag: `git tag vX.Y.Z`.
2. Push the tag `git push --tags`.
3. Bump the version number in https://github.com/tronbyt/homebrew-tronbyt/blob/main/Formula/tronbyt-server.rb and update the checksum.
