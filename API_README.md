# Tronbyt Server API Documentation

This directory contains the OpenAPI/Swagger specification for the Tronbyt Server API.

## Files

- `openapi.yaml` - Complete OpenAPI 3.0 specification
- `API_README.md` - This documentation file

## Viewing the API Documentation

### Option 1: Swagger UI (Recommended)

1. Open [Swagger Editor](https://editor.swagger.io/) in your browser
2. Copy the contents of `openapi.yaml` and paste it into the editor
3. The interactive documentation will be displayed with:
   - All endpoints organized by tags
   - Request/response examples
   - Interactive "Try it out" functionality
   - Schema definitions

### Option 2: Local Swagger UI

If you have Swagger UI installed locally:

```bash
# Install swagger-ui-serve globally
npm install -g swagger-ui-serve

# Serve the API documentation
swagger-ui-serve openapi.yaml
```

### Option 3: VS Code Extension

Install the "OpenAPI (Swagger) Editor" extension in VS Code to get syntax highlighting and validation.

## API Overview

The Tronbyt Server API provides endpoints for:

### Device Management
- **GET** `/v0/devices` - List all devices
- **GET** `/v0/devices/{device_id}` - Get device details
- **PATCH** `/v0/devices/{device_id}` - Update device settings

### Image Pushing
- **POST** `/v0/devices/{device_id}/push` - Push base64-encoded WebP image
- **POST** `/v0/devices/{device_id}/push_app` - Render and push app with config

### Installation Management
- **GET** `/v0/devices/{device_id}/installations` - List app installations
- **PATCH** `/v0/devices/{device_id}/installations/{installation_id}` - Update installation
- **PUT** `/v0/devices/{device_id}/installations/{installation_id}` - Alternative update method
- **DELETE** `/v0/devices/{device_id}/installations/{installation_id}` - Delete installation

## Authentication

The API supports two authentication methods:

### Bearer Token (Recommended)
```bash
curl -H "Authorization: Bearer your-api-key-here" \
     https://your-server.com/v0/devices
```

### Direct API Key
```bash
curl -H "Authorization: your-api-key-here" \
     https://your-server.com/v0/devices
```

## Common Use Cases

### 1. List All Devices
```bash
curl -H "Authorization: Bearer your-api-key" \
     https://your-server.com/v0/devices
```

### 2. Push an Image
```bash
curl -X POST \
     -H "Authorization: Bearer your-api-key" \
     -H "Content-Type: application/json" \
     -d '{"image": "base64-encoded-webp-data", "installationID": "my-image-123"}' \
     https://your-server.com/v0/devices/a1b2c3d4/push
```

### 3. Enable an App
```bash
curl -X PATCH \
     -H "Authorization: Bearer your-api-key" \
     -H "Content-Type: application/json" \
     -d '{"set_enabled": true}' \
     https://your-server.com/v0/devices/a1b2c3d4/installations/clock-123
```

### 4. Pin an App
```bash
curl -X PATCH \
     -H "Authorization: Bearer your-api-key" \
     -H "Content-Type: application/json" \
     -d '{"set_pinned": true}' \
     https://your-server.com/v0/devices/a1b2c3d4/installations/clock-123
```

### 5. Push an App with Configuration
```bash
curl -X POST \
     -H "Authorization: Bearer your-api-key" \
     -H "Content-Type: application/json" \
     -d '{
       "app_id": "clock",
       "config": {
         "timezone": "America/New_York",
         "format": "12h"
       },
       "installationID": "clock-123"
     }' \
     https://your-server.com/v0/devices/a1b2c3d4/push_app
```

## Data Models

### Device
- `id`: 8-character hexadecimal device ID
- `displayName`: Human-readable device name
- `brightness`: Brightness level (0-255)
- `autoDim`: Whether automatic dimming is enabled

### Installation
- `id`: Installation ID
- `appID`: App name/identifier

### App Configuration
Apps can have various configuration parameters depending on the app type. Common parameters include:
- `timezone`: Timezone string (e.g., "America/New_York")
- `format`: Display format (e.g., "12h" for 12-hour time)
- `location`: Location-specific settings
- Custom parameters as defined by each app

## Error Handling

The API returns standard HTTP status codes:

- **200**: Success
- **400**: Bad Request (invalid parameters, missing data)
- **401**: Unauthorized (invalid API key)
- **404**: Not Found (device/app not found)
- **500**: Internal Server Error

Error responses include descriptive messages:

```json
{
  "error": "Missing or invalid Authorization header"
}
```

## Rate Limiting

Currently, the API does not implement rate limiting, but it's recommended to:
- Avoid rapid successive requests
- Implement client-side rate limiting for production use
- Monitor API usage for abuse

## Security Considerations

1. **API Keys**: Keep your API keys secure and don't share them
2. **HTTPS**: Always use HTTPS in production
3. **Device IDs**: Device IDs are 8-character hexadecimal strings
4. **Image Data**: Base64-encoded WebP images should be properly validated

## Development

### Testing the API

You can test the API using:

1. **curl** (command line)
2. **Postman** (GUI)
3. **Swagger UI** (interactive documentation)
4. **Python requests** library
5. **JavaScript fetch** API

### Example Python Client

```python
import requests
import base64

# Configuration
API_KEY = "your-api-key-here"
BASE_URL = "https://your-server.com/v0"
DEVICE_ID = "a1b2c3d4"

headers = {
    "Authorization": f"Bearer {API_KEY}",
    "Content-Type": "application/json"
}

# List devices
response = requests.get(f"{BASE_URL}/devices", headers=headers)
print(response.json())

# Push an image
with open("image.webp", "rb") as f:
    image_data = base64.b64encode(f.read()).decode()

data = {
    "image": image_data,
    "installationID": "my-image-123"
}

response = requests.post(
    f"{BASE_URL}/devices/{DEVICE_ID}/push",
    headers=headers,
    json=data
)
print(response.text)
```

## Contributing

To update the API documentation:

1. Modify `openapi.yaml` with your changes
2. Validate the YAML syntax
3. Test the examples in Swagger UI
4. Update this README if needed

## Support

For API support:
- Check the server logs for detailed error messages
- Verify your API key is correct
- Ensure device IDs are valid 8-character hexadecimal strings
- Validate that image data is properly base64-encoded WebP format
