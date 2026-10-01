# Tronbyt Server API v0 Reference

Base URL: `http://<server>:8000`

## Authentication

All API endpoints require authentication via Bearer token or `key` query parameter:

```
Authorization: Bearer <api-key>
```
or
```
?key=<api-key>
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
  "colorOrder": "rgb",
  "disableTouch": false,
  "touchBeep": false,
  "startupSound": true,
  "wifiPowerSave": 0,
  "imageUrl": "http://example.com/image.webp",
  "hostname": "tronbyt.local",
  "sntpServer": "pool.ntp.org",
  "syslogAddr": "192.168.1.100:514"
}
```

`colorOrder` is one of `rgb`, `rbg`, `grb`, `gbr`, `brg`, `bgr` (case-insensitive; stored and sent lower-case). It requires firmware with `COLOR_ORDER` support; older firmware ignores it.

`disableTouch` and `touchBeep` only apply to the Tidbyt Gen2. `touchBeep` plays a short tone on the built-in speaker when the touch button is used and takes effect immediately; it requires firmware with touch beep support, older firmware ignores it. `startupSound` controls whether the startup jingle is played on boot.

**Response:** `200 OK` — `"Firmware settings updated."`

### Trigger Sound

```
POST /v0/devices/{id}/sound
Content-Type: application/json
```

Triggers a notification sound to play immediately on the device (via WebSocket if connected, or queued as pending for the next HTTP poll).

**Request:**
```json
{
  "sound": "chime"
}
```

Or with an app sound:
```json
{
  "app_id": "nfl_scores",
  "sound": "touchdown.mp3"
}
```

Or with an audio URL:
```json
{
  "url": "https://example.com/sound.mp3"
}
```

Or raw piezo tone string for piezo devices:
```json
{
  "sound": "523:120,659:120,784:220"
}
```

| Field | Required | Description |
|-------|----------|-------------|
| `sound` | No* | Sound identifier: default sound name (`chime`, `ding`, `bell`, `pop`, `alert`), app sound (`app:<app_id>:<filename>` or just filename when `app_id` is supplied), custom uploaded sound filename, full audio URL, or piezo frequency:duration tone string. |
| `app_id` | No | App identifier for resolving app-specific sounds. |
| `url` | No* | Alias for `sound` when passing an audio URL. |

*Either `sound` or `url` must be provided.

Based on the device's audio capability (`piezo` or `full`):
- `piezo` devices receive the frequency:duration tone string. Default sounds and app sounds with matching keywords (`ding`, `chime`, `bell`, `pop`, `alert`) map to tuned buzzer tones, with a pleasant 2-tone chime fallback for other app sounds.
- `full` devices receive the full audio stream URL (resolved against `/sounds/{appId}/{filename}` or external URL).
- `none` devices ignore sound playback.

**Response:** `200 OK` — `{"status":"ok","device":"...","sound":"...","payload":"..."}`

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
      "id": "my-clock",
      "appID": "clock",
      "enabled": true,
      "pinned": false,
      "pushed": false,
      "renderIntervalMin": 0,
      "displayTimeSec": 30,
      "lastRenderAt": 1704067200,
      "isInactive": false,

      "startTime": "09:00",
      "endTime": "17:00",
      "days": ["monday", "wednesday", "friday"],

      "useCustomRecurrence": false,
      "recurrenceType": "",
      "recurrenceInterval": 0,
      "recurrencePattern": null,
      "recurrenceStartDate": null,
      "recurrenceEndDate": null,

      "autoPin": false,
      "colorFilter": null,
      "showFullAnimation": null,
      "notificationSound": "chime",
      "notificationSoundTrigger": "on_change"
    }
  ]
}
```

App `config` is **not** returned. It holds whatever the app's schema defines,
which for many apps includes API keys and OAuth tokens, and a device API key is
a lower bar than a logged-in session. Config can be written (see below) but
never read back.

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

Update installation settings. All fields are optional; omitting one leaves it
alone.

**Request:**
```json
{
  "enabled": true,
  "pinned": false,
  "renderIntervalMin": 5,
  "displayTimeSec": 30,

  "startTime": "09:00",
  "endTime": "17:00",
  "days": ["monday", "wednesday", "friday"],

  "autoPin": false,
  "colorFilter": "dimmed",
  "showFullAnimation": "true",
  "notificationSound": "chime",
  "notificationSoundTrigger": "on_change",

  "config": { "timezone": "America/New_York" }
}
```

| Field | Notes |
|-------|-------|
| `startTime` / `endTime` | `"HH:MM"`. `""` clears. A start later than the end wraps overnight. |
| `days` | Lowercase day names. `[]` means every day. |
| `colorFilter` | One of the filters the app config page offers. `""` or `"inherit"` falls back to the device setting. |
| `showFullAnimation` | `"true"` / `"false"`, or `"auto"` to inherit. Lets an animation run past the app's display time. |
| `notificationSound` | Sound name (`"chime"`, `"ding"`, `"bell"`, `"pop"`, `"alert"`), custom uploaded sound filename, full URL (`"http(s)://..."`), piezo tone string, or `""` / `null` to disable. |
| `notificationSoundTrigger` | `"on_change"` (play only when rendered WebP image hash changes) or `"every_render"` (play every render cycle). Defaults to `"on_change"`. |
| `config` | Replaces the app's whole config map — there is no per-key merge, so send the full object. **Write-only:** it is never returned by GET or in this response. |

**Response:** Updated installation object, in the same shape `GET` returns.

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
| `sound` | No | Sound to trigger simultaneously with the push (e.g. `"touchdown.mp3"`, `"chime"`, or URL) |

**Response:** `200 OK` — `"App pushed."`

**Examples:**

Push with explicit app_id and sound (e.g. sports score alert):
```bash
curl -X POST \
  -H "Authorization: Bearer <key>" \
  -H "Content-Type: application/json" \
  -d '{"app_id": "nfl_scores", "sound": "touchdown.mp3", "background": false}' \
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

Pushes a base64-encoded WebP image directly to the device, optionally triggering a simultaneous sound alert.

**Request:**
```json
{
  "installationID": "my-image",
  "image": "<base64-encoded-webp>",
  "background": false,
  "sound": "chime"
}
```

| Field | Required | Description |
|-------|----------|-------------|
| `installationID` | No | Identifier for the pushed image |
| `image` | Yes | Base64-encoded WebP image bytes |
| `background` | No | If `true`, saves without interrupting (default: `false`) |
| `sound` | No | Optional sound identifier to play immediately with the image |

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

### Trigger a notification sound programmatically

Via curl / Home Assistant / Webhook:
```bash
curl -X POST \
  -H "Authorization: Bearer <key>" \
  -H "Content-Type: application/json" \
  -d '{"sound": "alert"}' \
  http://localhost:8000/v0/devices/gen1/sound
```

Via Pixlet Starlark app:
```python
load("http.star", "http")

def main(config):
    server = config.get("tronbyt_url", "http://tronbyt.local:8000")
    device_id = config.get("device_id")
    api_key = config.get("api_key")
    http.post(
        "%s/v0/devices/%s/sound?key=%s" % (server, device_id, api_key),
        json_body = {"sound": "ding"},
    )
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
