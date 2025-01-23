# Tronbyt Server

This is a Flask app for managing your apps on your Tronbyt (flashed Tidbyt). This project is designed to run your Tronbyt/Tidbyt completely locally without relying on the backend servers operated by Tidbyt.

## Getting Started

### Prerequisites

- Docker
- Docker Compose

### Installation

1. Clone the repository:
    ```sh
    git clone <repository-url>
    cd <repository-directory>
    ```

2. Copy the example environment file and modify it as needed:
    ```sh
    cp .env.example .env
    ```

3. Set the `DOMAIN` value in the `.env` file if not running locally.

### Running the Application

1. Build and start the Docker containers:
    ```sh
    docker-compose up
    ```

2. Access the web app at [http://localhost:8000](http://localhost:8000) (or your configured domain) with the default login credentials:
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

- Ensure that the `DOMAIN` value is set in the `.env` file if you are not running the application locally.
