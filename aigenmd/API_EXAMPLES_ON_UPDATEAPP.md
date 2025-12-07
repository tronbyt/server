# API Examples on Update App Page

## Overview
Added a comprehensive API examples section to the Update App page that displays ready-to-use curl commands for controlling the app via the API.

## Implementation

### Location
The API examples box appears at the bottom of the Update App page, after the Save/Delete buttons.

### Features

**Four API Operations:**
1. **Enable App** - Turn the app on
2. **Disable App** - Turn the app off
3. **Pin App** - Pin the app to always display
4. **Unpin App** - Remove the pin from the app

### Visual Design

**Box Styling:**
- Dark background (#2a2a2a)
- Green left border (#4CAF50)
- Rounded corners (8px)
- 30px top margin for separation

**Code Blocks:**
- Dark background (#1e1e1e)
- Light gray text (#e0e0e0)
- Horizontal scrolling for long commands
- Monospace font
- 15px padding

**Headers:**
- Green for positive actions (Enable, Pin)
- Orange for negative actions (Disable, Unpin)

## Changes Made

### 1. Manager Route (`tronbyt_server/manager.py`)

**Lines 861-869:**
```python
device = g.user["devices"][device_id]

return render_template(
    "manager/updateapp.html",
    app=app,
    device=device,  # NEW - Pass device to template
    device_id=device_id,
    config=json.dumps(app.get("config", {}), indent=4),
)
```

Added `device` to the template context so we can access the device API key.

### 2. Update App Template (`tronbyt_server/templates/manager/updateapp.html`)

**Lines 209-257:**
Added complete API examples section with four curl commands.

## Example Output

### Enable App
```bash
curl -X PATCH \
  -H "Authorization: Bearer abc123xyz..." \
  -H "Content-Type: application/json" \
  -d '{"set_enabled": true}' \
  http://localhost:5000/v0/devices/my-device/installations/clock-123
```

### Disable App
```bash
curl -X PATCH \
  -H "Authorization: Bearer abc123xyz..." \
  -H "Content-Type: application/json" \
  -d '{"set_enabled": false}' \
  http://localhost:5000/v0/devices/my-device/installations/clock-123
```

### Pin App
```bash
curl -X PATCH \
  -H "Authorization: Bearer abc123xyz..." \
  -H "Content-Type: application/json" \
  -d '{"set_pinned": true}' \
  http://localhost:5000/v0/devices/my-device/installations/clock-123
```

### Unpin App
```bash
curl -X PATCH \
  -H "Authorization: Bearer abc123xyz..." \
  -H "Content-Type: application/json" \
  -d '{"set_pinned": false}' \
  http://localhost:5000/v0/devices/my-device/installations/clock-123
```

## Dynamic Values

The curl commands automatically include:
- **API Key**: `{{ device.get('api_key', 'YOUR_API_KEY') }}`
- **Server URL**: `{{ request.url_root }}`
- **Device ID**: `{{ device_id }}`
- **Installation ID**: `{{ app['iname'] }}`

### API Key Handling
- If device has an API key, it's shown in the command
- If no API key, shows placeholder "YOUR_API_KEY"
- Note at bottom reminds users to replace if needed

### Server URL
- Uses `request.url_root` to get the current server URL
- Works with localhost, production domains, custom ports
- Examples:
  - `http://localhost:5000/`
  - `https://tronbyt.example.com/`
  - `http://192.168.1.100:8080/`

## User Benefits

### 1. Copy-Paste Ready
- Commands are complete and ready to use
- No manual editing needed (if API key is set)
- Just copy and paste into terminal

### 2. Learning Tool
- Shows exact API endpoint structure
- Demonstrates proper headers and JSON format
- Helps users understand the API

### 3. Quick Testing
- Test API functionality immediately
- Verify app control works
- Debug integration issues

### 4. Documentation
- Self-documenting API
- Always up-to-date with current values
- No need to look up documentation

## Internationalization

All text is wrapped in `{{ _() }}` for translation:
- Section title: "API Examples"
- Instructions: "Use these curl commands..."
- Operation names: "Enable App", "Disable App", etc.
- Note text: "Replace YOUR_API_KEY..."

## Styling Details

### Section Header
```css
color: #4CAF50;
margin-top: 0;
```

### Instructions
```css
color: #b0b0b0;
margin-bottom: 20px;
```

### Operation Headers
```css
font-size: 1.1em;
margin-bottom: 10px;
color: #4CAF50 (enable/pin) or #ff9800 (disable/unpin)
```

### Code Blocks
```css
background-color: #1e1e1e;
padding: 15px;
border-radius: 4px;
overflow-x: auto;
color: #e0e0e0;
font-size: 0.9em;
```

### Note Text
```css
color: #888;
font-size: 0.9em;
margin-top: 20px;
```

## Technical Details

### Template Variables Used
- `device` - Device object with API key
- `device_id` - Device ID string
- `app['iname']` - Installation ID
- `request.url_root` - Server base URL

### Jinja2 Features
- `{{ device.get('api_key', 'YOUR_API_KEY') }}` - Safe dictionary access with default
- `{{ _('text') }}` - Translation function
- Multi-line strings in `<pre>` tags

### HTTP Details
- **Method**: PATCH (partial update)
- **Endpoint**: `/v0/devices/{device_id}/installations/{installation_id}`
- **Headers**: Authorization (Bearer token), Content-Type (application/json)
- **Body**: JSON with operation key

## Security Considerations

### API Key Display
- Shows actual API key if available
- Helps users who have access to the page
- Users already authenticated to see this page
- API key needed to actually use the commands

### Best Practices
- Commands use HTTPS in production (via `request.url_root`)
- Bearer token authentication
- JSON content type specified
- Proper REST semantics (PATCH for updates)

## Future Enhancements

### Possible Improvements

1. **Copy Button**: Add button to copy command to clipboard
   ```html
   <button onclick="copyToClipboard(this)">Copy</button>
   ```

2. **Toggle Visibility**: Collapsible section like config/debug
   ```html
   <button id="toggleApiBtn">Show API Examples</button>
   ```

3. **More Operations**: Add examples for other API endpoints
   - Push image
   - Update configuration
   - Delete installation

4. **Language Selection**: Show examples in different languages
   - Python (requests library)
   - JavaScript (fetch API)
   - PowerShell

5. **Response Examples**: Show expected API responses
   ```json
   {"status": "success", "message": "App enabled."}
   ```

6. **Error Examples**: Show common error responses
   ```json
   {"error": "Invalid API key"}
   ```

## Testing Checklist

- [ ] API examples box appears on update app page
- [ ] All four commands are displayed
- [ ] Device API key is shown correctly
- [ ] Server URL is correct
- [ ] Device ID is correct
- [ ] Installation ID is correct
- [ ] Commands are properly formatted
- [ ] Code blocks are scrollable
- [ ] Colors match design (green/orange)
- [ ] Text is translatable
- [ ] Commands work when copied

## Files Modified

1. **`tronbyt_server/manager.py`** (lines 861-869)
   - Added `device` to template context

2. **`tronbyt_server/templates/manager/updateapp.html`** (lines 209-257)
   - Added API examples section
   - Four curl command examples
   - Styling and formatting

## Related Features

- API endpoint: `/v0/devices/{device_id}/installations/{installation_id}` (PATCH)
- Operations: `set_enabled`, `set_pinned`
- Authentication: Bearer token (device API key)

## Summary

Added a comprehensive API examples section to the Update App page that provides ready-to-use curl commands for:
- Enabling/disabling the app
- Pinning/unpinning the app

The commands include all necessary values (API key, device ID, installation ID, server URL) and are formatted for easy copy-paste usage. This makes the API more discoverable and easier to use for developers and power users.
