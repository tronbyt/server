# Tronbyt Server API v0 Reference

Base URL: `http://<server>:8000`

## Authentication

All API endpoints require authentication via Bearer token:

```
Authorization: Bearer <api-key>
```

API keys are generated per-device in the web UI. Each key is scoped to a single device.

---

## Devices

### List Devices

```
GET /v0/devices
```

Returns all devices accessible to the authenticated API key.

**Response:**
```json
{
  "devices": [
    {
      "id": "gen1",
      "type": "gen1",
      "displayName": "My Tronbyt",
      "notes": "",
      "intervalSec": 30,
      "brightness": 100,
      "nightMode": {
        "enabled": false,
        "app": "",
        "startTime": "",
        "endTime": "",
        "brightness": 0
      },
      "dimMode": {
        "startTime": null,
        "brightness": null
      },
      "pinnedApp": null,
      "interstitial": {
        "enabled": false,
        "app": null
      },
      "lastSeen": "2024-01-01T00:00:00Z",
      "info": {
        "firmwareVersion": "1.0.0",
        "firmwareType": "gen1",
        "protocolVersion": 1,
        "macAddress": "aa:bb:cc:dd:ee:ff"
      },
      "autoDim": false
    }
  ]
}
```

### Get Device

```
GET /v0/devices/{id}
```

Returns details for a specific device.

### Update Device

```
PATCH /v0/devices/{id}
Content-Type: application/json
```

Update device settings. All fields are optional.

**Request:**
```json
{
  "brightness": 80,
  "intervalSec": 60,
  "nightModeEnabled": true,
  "nightModeApp": "clock",
  "nightModeBrightness": 10,
  "nightModeStartTime": "22:00",
  "nightModeEndTime": "07:00",
  "dimModeStartTime": "20:00",
  "dimModeBrightness": 30,
  "pinnedApp": "weather"
}
```

**Response:** Updated device payload (same shape as GET).

### Reboot Device

```
POST /v0/devices/{id}/reboot
```

Sends a reboot command to the device via WebSocket. Returns immediately; the device reboots asynchronously.

**Response:** `200 OK` — `"Reboot command sent."`

### Update Firmware Settings

```
POST /v0/devices/{id}/update_firmware_settings
Content-Type: application/json
```

Update low-level firmware settings. All fields are optional.

**Request:**
```json
{
  "skipDisplayVersion": true,
  "skipBootAnimation": true,
  "preferIPv6": false,
  "apMode": false,
  "swapColors": false,
  "wifiPowerSave": 0,
  "imageUrl": "http://example.com/image.webp",
  "hostname": "tronbyt.local",
  "sntpServer": "pool.ntp.org",
  "syslogAddr": "192.168.1.100:514"
}
```

**Response:** `200 OK` — `"Firmware settings updated."`

---

## Installations (Apps)

### List Installations

```
GET /v0/devices/{id}/installations
```

Returns all app installations on a device.

**Response:**
```json
{
  "installations": [
    {
      "id": 1,
      "iname": "my-clock",
      "name": "Clock",
      "app_id": "clock",
      "enabled": true,
      "display_time": 30,
      "u_interval": 0,
      "last_render": "2024-01-01T00:00:00Z",
      "config": {
        "timezone": "America/New_York"
      },
      "pinned": false
    }
  ]
}
```

### Get Installation

```
GET /v0/devices/{id}/installations/{iname}
```

Returns details for a specific app installation.

### Update Installation

```
PATCH /v0/devices/{id}/installations/{iname}
Content-Type: application/json
```

Update installation settings. All fields are optional.

**Request:**
```json
{
  "enabled": true,
  "pinned": false,
  "renderIntervalMin": 5,
  "displayTimeSec": 30
}
```

**Response:** Updated installation object.

### Delete Installation

```
DELETE /v0/devices/{id}/installations/{iname}
```

Removes an app installation and its associated WebP files.

**Response:** `200 OK` — `"App deleted."`

---

## Push (Render & Display)

### Push App

```
POST /v0/devices/{id}/push_app
Content-Type: application/json
```

Renders an app and pushes it to the device. If `background` is `false`, the device immediately interrupts its current display and shows the pushed image.

**Request:**
```json
{
  "app_id": "clock",
  "installationID": "my-clock",
  "config": {
    "timezone": "America/New_York"
  },
  "background": false
}
```

| Field | Required | Description |
|-------|----------|-------------|
| `app_id` | No* | The app identifier (e.g. `"clock"`, `"weather"`). Required only if `installationID` is not provided or references a non-existent installation. |
| `installationID` | No | Installation name. If provided and valid, the app path is inferred from the existing installation, and its saved config is used if `config` is omitted. |
| `config` | No | App configuration. If omitted and `installationID` is provided, uses saved config from that installation. |
| `background` | No | If `true`, saves the image without interrupting the device (default: `false`) |

**Response:** `200 OK` — `"App pushed."`

**Examples:**

Push with explicit app_id (required when not using installationID):
```bash
curl -X POST \
  -H "Authorization: Bearer <key>" \
  -H "Content-Type: application/json" \
  -d '{"app_id": "clock", "config": {"timezone": "America/New_York"}, "background": false}' \
  http://localhost:8000/v0/devices/gen1/push_app
```

Activate an existing installation (app_id inferred from installation):
```bash
curl -X POST \
  -H "Authorization: Bearer <key>" \
  -H "Content-Type: application/json" \
  -d '{"installationID": "851", "background": false}' \
  http://localhost:8000/v0/devices/gen1/push_app
```

### Push Raw Image

```
POST /v0/devices/{id}/push
Content-Type: application/json
```

Pushes a base64-encoded WebP image directly to the device.

**Request:**
```json
{
  "installationID": "my-image",
  "image": "<base64-encoded-webp>",
  "background": false
}
```

| Field | Required | Description |
|-------|----------|-------------|
| `installationID` | No | Identifier for the pushed image |
| `image` | Yes | Base64-encoded WebP image bytes |
| `background` | No | If `true`, saves without interrupting (default: `false`) |

**Response:** `200 OK` — `"WebP received."`

**Example:**
```bash
curl -X POST \
  -H "Authorization: Bearer <key>" \
  -H "Content-Type: application/json" \
  -d '{
    "installationID": "custom",
    "image": "'$(base64 -w0 image.webp)'",
    "background": false
  }' \
  http://localhost:8000/v0/devices/gen1/push
```

---

## Common Patterns

### Activate an existing app immediately

1. Push using the installationID — app path and config are inferred from the installation:
```bash
curl -X POST \
  -H "Authorization: Bearer <key>" \
  -H "Content-Type: application/json" \
  -d '{"installationID": "851", "background": false}' \
  http://localhost:8000/v0/devices/gen1/push_app
```

### Enable/disable an app

```bash
curl -X PATCH \
  -H "Authorization: Bearer <key>" \
  -H "Content-Type: application/json" \
  -d '{"enabled": false}' \
  http://localhost:8000/v0/devices/gen1/installations/851
```

### Pin an app (always show it)

```bash
curl -X PATCH \
  -H "Authorization: Bearer <key>" \
  -H "Content-Type: application/json" \
  -d '{"pinned": true}' \
  http://localhost:8000/v0/devices/gen1/installations/851
```

### Set device brightness

```bash
curl -X PATCH \
  -H "Authorization: Bearer <key>" \
  -H "Content-Type: application/json" \
  -d '{"brightness": 50}' \
  http://localhost:8000/v0/devices/gen1
```

---

## Error Responses

| Status | Meaning |
|--------|---------|
| `400` | Invalid JSON, missing fields, or bad values |
| `401` | Missing or invalid API key |
| `404` | Device or installation not found |
| `500` | Internal server error |

Error responses are plain text with a brief message.
