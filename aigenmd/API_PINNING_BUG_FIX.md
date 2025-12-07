# API Pinning Bug Fix

## Problem

The API pinning endpoint was returning "App pinned." but the app wasn't actually being pinned. The web interface would not show the app as pinned after using the API command.

## Root Cause

The issue was in how the device object was being modified and saved in `tronbyt_server/api.py`.

### Original Code (Buggy)
```python
# Get user for saving changes
user = db.get_user_by_device_id(device_id)
if not user:
    abort(HTTPStatus.NOT_FOUND, description="User not found")

apps = device.get("apps", {})  # Using 'device' from line 236
if installation_id not in apps:
    abort(HTTPStatus.NOT_FOUND, description="App not found")

if set_pinned:
    # Pin the app
    device["pinned_app"] = installation_id  # ❌ Modifying wrong device object!
    db.save_user(user)
    return Response("App pinned.", status=200)
```

### The Problem

1. **Line 236**: `device = db.get_device_by_id(device_id)`
   - This retrieves a device object from the database

2. **Line 295**: `user = db.get_user_by_device_id(device_id)`
   - This retrieves the user object that owns the device

3. **Line 305**: `device["pinned_app"] = installation_id`
   - This modifies the `device` variable from line 236
   - But this is a **separate copy** of the device, not the one in `user["devices"]`

4. **Line 306**: `db.save_user(user)`
   - This saves the user object
   - But the user's device dictionary was never modified!
   - The changes to `device` are lost

### Why It Happened

The `db.get_device_by_id()` function returns a device object:

```python
def get_device_by_id(device_id: str) -> Optional[Device]:
    for user in get_all_users():
        device = user.get("devices", {}).get(device_id)
        if device:
            return device  # Returns the device object
    return None
```

This returns a reference to the device, but when we later get the user separately and save it, we're not saving the same device object that we modified.

## Solution

Modify the device through the user's device dictionary, not through the standalone device variable.

### Fixed Code
```python
# Get user for saving changes
user = db.get_user_by_device_id(device_id)
if not user:
    abort(HTTPStatus.NOT_FOUND, description="User not found")

# Get device from user's devices (not the standalone device variable)
user_device = user["devices"].get(device_id)
if not user_device:
    abort(HTTPStatus.NOT_FOUND, description="Device not found in user data")

apps = user_device.get("apps", {})  # ✅ Using user_device
if installation_id not in apps:
    abort(HTTPStatus.NOT_FOUND, description="App not found")

if set_pinned:
    # Pin the app
    user_device["pinned_app"] = installation_id  # ✅ Modifying correct device!
    db.save_user(user)  # ✅ Saves the modified user with updated device
    return Response("App pinned.", status=200)
```

### Key Changes

1. **Line 300**: Get device from `user["devices"]` instead of using the standalone `device` variable
2. **Line 310**: Modify `user_device["pinned_app"]` instead of `device["pinned_app"]`
3. **Line 315**: Check `user_device.get("pinned_app")` for unpinning

## Why This Works

```
user = {
    "username": "john",
    "devices": {
        "device-123": {           ← This is user_device
            "id": "device-123",
            "name": "My Device",
            "apps": {...},
            "pinned_app": None     ← We modify this
        }
    }
}

# When we do:
user_device = user["devices"]["device-123"]
user_device["pinned_app"] = "clock-123"

# We're modifying the device inside the user object
# So when we save the user, the changes persist!
db.save_user(user)  # ✅ Saves with pinned_app set
```

## Changes Made

**File: `tronbyt_server/api.py` (lines 286-320)**

### Before
```python
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
```

### After
```python
# Get user for saving changes
user = db.get_user_by_device_id(device_id)
if not user:
    abort(HTTPStatus.NOT_FOUND, description="User not found")

# Get device from user's devices (not the standalone device variable)
user_device = user["devices"].get(device_id)
if not user_device:
    abort(HTTPStatus.NOT_FOUND, description="Device not found in user data")

apps = user_device.get("apps", {})
if installation_id not in apps:
    abort(HTTPStatus.NOT_FOUND, description="App not found")

if set_pinned:
    # Pin the app
    user_device["pinned_app"] = installation_id
    db.save_user(user)
    return Response("App pinned.", status=200)
```

## Testing

### Test Pin Operation
```bash
curl -X PATCH \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"set_pinned": true}' \
  http://localhost:5000/v0/devices/DEVICE_ID/installations/INSTALLATION_ID
```

**Expected:**
- Response: "App pinned."
- Web interface shows app as pinned
- Device displays only the pinned app
- Badge appears next to device name

### Test Unpin Operation
```bash
curl -X PATCH \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"set_pinned": false}' \
  http://localhost:5000/v0/devices/DEVICE_ID/installations/INSTALLATION_ID
```

**Expected:**
- Response: "App unpinned."
- Web interface shows app as not pinned
- Device resumes normal app rotation
- Badge disappears from device name

## Related Code

### Web Interface (Working Correctly)
The web interface in `manager.py` already does this correctly:

```python
@bp.get("/<string:device_id>/<string:iname>/toggle_pin")
@login_required
def toggle_pin(device_id: str, iname: str) -> ResponseReturnValue:
    user = g.user
    device = user["devices"][device_id]  # ✅ Gets device from user

    if device.get("pinned_app") == iname:
        device.pop("pinned_app", None)  # ✅ Modifies device in user
    else:
        device["pinned_app"] = iname    # ✅ Modifies device in user

    db.save_user(user)  # ✅ Saves correctly
    return redirect(url_for("manager.index"))
```

### Why set_enabled Worked
The `set_enabled` operation worked because it uses `db.save_app()` which handles the device lookup internally:

```python
if set_enabled:
    app["enabled"] = True
    app["last_render"] = 0
    if db.save_app(device_id, app):  # ✅ save_app handles device lookup
        return Response("App Enabled.", status=200)
```

## Lessons Learned

### 1. Object References in Python
When you get an object from a dictionary and modify it, you need to make sure you're modifying the object that will be saved, not a copy or separate reference.

### 2. Consistency in Code Patterns
The `set_enabled` operation used `db.save_app()` which worked correctly. The `set_pinned` operation tried to use `db.save_user()` directly but didn't properly update the user's device.

### 3. Testing API Endpoints
Always test API endpoints end-to-end:
1. Call the API
2. Check the response
3. **Verify the change persisted** (check web interface, database, etc.)

## Prevention

### Code Review Checklist
When modifying nested objects:
- [ ] Are you modifying the object that will be saved?
- [ ] Is the object part of a larger structure (user → devices → device)?
- [ ] Does the save operation include your modifications?
- [ ] Have you tested that changes persist after save?

### Better Pattern
Consider creating a helper function:

```python
def update_device_property(device_id: str, property_name: str, value: Any) -> bool:
    """Update a device property and save it."""
    user = db.get_user_by_device_id(device_id)
    if not user:
        return False

    device = user["devices"].get(device_id)
    if not device:
        return False

    device[property_name] = value
    db.save_user(user)
    return True
```

## Summary

Fixed the API pinning bug by ensuring we modify the device object that's part of the user's device dictionary, not a separate device object. The key was to use `user["devices"][device_id]` instead of the standalone `device` variable when making modifications that need to persist.

**Status:** ✅ Fixed and tested
