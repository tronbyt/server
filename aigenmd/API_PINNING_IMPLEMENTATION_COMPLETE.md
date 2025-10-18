# API App Pinning Implementation - Complete

## Overview
Successfully implemented app pinning functionality in the API (`api.py`) using Option 1 - extending the existing PATCH endpoint.

## Implementation Summary

### What Was Added
Added `set_pinned` operation to the `handle_patch_device_app()` endpoint at line 287-316 in `tronbyt_server/api.py`.

### Code Changes

**File: `tronbyt_server/api.py`**

Added the following code block after the `set_enabled` handler:

```python
# Handle the set_pinned json command
elif request.json is not None and "set_pinned" in request.json:
    set_pinned = request.json["set_pinned"]
    if not isinstance(set_pinned, bool):
        return Response(
            "Invalid value for set_pinned. Must be a boolean.", status=400
        )

    # Get user for saving changes
    user = db.get_user_by_device_id(device_id)
    if not user:
        abort(HTTPStatus.NOT_FOUND, description="User not found")

    apps = device.get("apps", {})
    if installation_id not in apps:
        abort(HTTPStatus.NOT_FOUND, description="App not found")

    if set_pinned:
        # Pin the app
        device["pinned_app"] = installation_id
        db.save_user(user)
        return Response("App pinned.", status=200)
    else:
        # Unpin the app (only if it's currently pinned)
        if device.get("pinned_app") == installation_id:
            device.pop("pinned_app", None)
            db.save_user(user)
            return Response("App unpinned.", status=200)
        else:
            return Response("App is not pinned.", status=200)
```

### Key Features

1. **Validation**: Checks that `set_pinned` is a boolean value
2. **Authentication**: Uses existing API key authentication
3. **Authorization**: Verifies user owns the device
4. **Safety**: Sanitizes installation_id to prevent path traversal
5. **Persistence**: Saves changes via `db.save_user(user)`
6. **Consistency**: Matches the web interface behavior

## API Usage

### Endpoint
```
PATCH /v0/devices/{device_id}/installations/{installation_id}
```

### Authentication
```
Authorization: Bearer YOUR_API_KEY
```

### Pin an App

**Request:**
```bash
curl -X PATCH \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"set_pinned": true}' \
  https://your-server.com/v0/devices/DEVICE_ID/installations/INSTALLATION_ID
```

**Response (Success):**
```
HTTP/1.1 200 OK
App pinned.
```

### Unpin an App

**Request:**
```bash
curl -X PATCH \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"set_pinned": false}' \
  https://your-server.com/v0/devices/DEVICE_ID/installations/INSTALLATION_ID
```

**Response (Success):**
```
HTTP/1.1 200 OK
App unpinned.
```

**Response (App Not Pinned):**
```
HTTP/1.1 200 OK
App is not pinned.
```

## Error Responses

### Invalid Boolean Value
```
HTTP/1.1 400 Bad Request
Invalid value for set_pinned. Must be a boolean.
```

### Missing Authorization
```
HTTP/1.1 400 Bad Request
Missing or invalid Authorization header
```

### Invalid API Key
```
HTTP/1.1 404 Not Found
```

### App Not Found
```
HTTP/1.1 404 Not Found
App not found
```

### User Not Found
```
HTTP/1.1 404 Not Found
User not found
```

## How It Works

### Pin Operation (`set_pinned: true`)
1. Validates the request and authentication
2. Checks that the app exists in the device's app list
3. Sets `device["pinned_app"] = installation_id`
4. Saves the user data to persist the change
5. Returns success message

### Unpin Operation (`set_pinned: false`)
1. Validates the request and authentication
2. Checks if the app is currently pinned
3. If pinned, removes the `pinned_app` field from device
4. Saves the user data to persist the change
5. Returns appropriate message

### Behavior Notes
- Only one app can be pinned at a time
- Pinning a new app automatically replaces any previously pinned app
- Unpinning only works if the specified app is currently pinned
- Pinned apps bypass normal rotation and are always displayed
- Pinned apps are shown regardless of enabled/schedule status

## Integration with Existing System

### Data Storage
- Uses existing `device["pinned_app"]` field (already defined in `models/device.py`)
- No database schema changes required
- Stored as part of the user's device configuration

### Display Logic
The existing display logic in `manager.py` (lines 1254-1286) already handles pinned apps:

```python
# Check for pinned app first - this short-circuits all other app selection logic
pinned_app_iname = device.get("pinned_app")
is_pinned_app = False
if pinned_app_iname and pinned_app_iname in apps:
    current_app.logger.debug(f"Using pinned app: {pinned_app_iname}")
    app = apps[pinned_app_iname]
    is_pinned_app = True
    # For pinned apps, we don't update last_app_index since we're not cycling
else:
    # Normal app selection logic
    ...
```

### Consistency with Web Interface
The API implementation matches the web interface behavior:
- Same data structure (`device["pinned_app"]`)
- Same persistence mechanism (`db.save_user(user)`)
- Same toggle logic (pin/unpin)
- Same priority (pinned apps always display)

## Testing Checklist

- [x] Code implemented
- [ ] Pin an app via API
- [ ] Verify app is pinned in web interface
- [ ] Verify pinned app displays on device
- [ ] Unpin app via API
- [ ] Verify app is unpinned in web interface
- [ ] Pin different app (verify old pin is replaced)
- [ ] Try to pin non-existent app (verify 404)
- [ ] Try with invalid API key (verify 404)
- [ ] Try with invalid boolean value (verify 400)
- [ ] Verify pinned app bypasses rotation
- [ ] Verify pinned app shows even when disabled

## Example Test Scenarios

### Scenario 1: Pin an App
```bash
# Pin app with installation_id "clock-123"
curl -X PATCH \
  -H "Authorization: Bearer abc123" \
  -H "Content-Type: application/json" \
  -d '{"set_pinned": true}' \
  http://localhost:5000/v0/devices/my-device/installations/clock-123

# Expected: "App pinned."
# Verify: Device always shows clock app
```

### Scenario 2: Switch Pinned App
```bash
# Pin a different app
curl -X PATCH \
  -H "Authorization: Bearer abc123" \
  -H "Content-Type: application/json" \
  -d '{"set_pinned": true}' \
  http://localhost:5000/v0/devices/my-device/installations/weather-456

# Expected: "App pinned."
# Verify: Device now shows weather app (clock is no longer pinned)
```

### Scenario 3: Unpin App
```bash
# Unpin the weather app
curl -X PATCH \
  -H "Authorization: Bearer abc123" \
  -H "Content-Type: application/json" \
  -d '{"set_pinned": false}' \
  http://localhost:5000/v0/devices/my-device/installations/weather-456

# Expected: "App unpinned."
# Verify: Device resumes normal app rotation
```

### Scenario 4: Error Handling
```bash
# Try to pin with invalid value
curl -X PATCH \
  -H "Authorization: Bearer abc123" \
  -H "Content-Type: application/json" \
  -d '{"set_pinned": "yes"}' \
  http://localhost:5000/v0/devices/my-device/installations/clock-123

# Expected: 400 Bad Request
# Message: "Invalid value for set_pinned. Must be a boolean."
```

## Files Modified

1. **`tronbyt_server/api.py`** (lines 287-316)
   - Added `set_pinned` operation handler
   - Integrated with existing PATCH endpoint
   - Added user lookup for saving changes

## Related Files (No Changes Needed)

- **`tronbyt_server/models/device.py`** - Already has `pinned_app` field
- **`tronbyt_server/db.py`** - Uses existing `save_user()` function
- **`tronbyt_server/manager.py`** - Display logic already handles pinned apps

## Benefits

1. **RESTful**: Uses PATCH for partial resource updates
2. **Consistent**: Matches existing `set_enabled` pattern
3. **Simple**: No new endpoints or routes needed
4. **Secure**: Uses existing authentication and authorization
5. **Compatible**: Works with existing web interface
6. **Maintainable**: Clear, documented code

## Future Enhancements

Consider these optional improvements:

1. **Return pinned status in installation list:**
   ```python
   "pinned": installation_id == device.get("pinned_app")
   ```

2. **Return pinned app in device payload:**
   ```python
   "pinnedApp": device.get("pinned_app")
   ```

3. **Add JSON response with more details:**
   ```python
   return Response(
       json.dumps({"pinned": True, "message": "App pinned."}),
       status=200,
       mimetype="application/json"
   )
   ```

## Documentation

- Full implementation guide: `API_APP_PINNING_IMPLEMENTATION.md`
- This completion summary: `API_PINNING_IMPLEMENTATION_COMPLETE.md`

## Status

✅ **Implementation Complete**

The app pinning functionality is now available via the API and ready for testing!

