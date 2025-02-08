# Tronbyt Server

This is a Flask app for managing your apps on your Tronbyt (flashed Tidbyt). This project is designed to run your Tronbyt/Tidbyt completely locally without relying on the backend servers operated by Tidbyt.

## Getting Started

If you've been invited to use my public instance of this server login there and skip to the quickstart guide.

### Prerequisites

- Docker
- Docker Compose (optional, but recommended)

### Installation

It is possible to just start the server on the default ports in the background with a one-liner:

```sh
docker run -d -e SERVER_HOSTNAME_OR_IP=<YOUR_SETTING_HERE> -e SERVER_PORT=8000 -e PIXLET_SERVE_PORT1=5100 -e PRODUCTION=1 -p 8000:8000 -p 5100:5100 -p 5101:5101 ghcr.io/tavdog/tronbyt-server
```

That said, the recommended installation method uses Docker Compose with a configuration file for your settings:

1. Download the [Compose project](https://raw.githubusercontent.com/tavdog/tronbyt-server/refs/heads/master/docker-compose.yaml) (or use the contents to add a service to an existing project).

2. Copy the [example environment file](https://raw.githubusercontent.com/tavdog/tronbyt-server/refs/heads/master/.env.example) and modify it as needed:

   ```sh
   curl https://raw.githubusercontent.com/tavdog/tronbyt-server/refs/heads/master/.env.example > .env
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
- Do not try to run this server over HTTPS. It requires a pixlet serve subprocess to configure the apps and it only works over http and you can't jump from https to http in most browsers.

### Development

1. Clone the repository:

   ```sh
   git clone https://github.com/tavdog/tronbyt-server.git
   cd tronbyt-server
   ```

2. Build and run the image using the local copy:

    ```sh
    docker compose -f docker-compose.dev.yaml up -d --build
    ```
