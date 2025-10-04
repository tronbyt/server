# Dim Mode Feature Documentation

## Overview
The dim mode feature allows devices to automatically dim their brightness at a specified time without entering full night mode. This is useful for gradually reducing brightness in the evening before bedtime.

## Feature Hierarchy
The brightness system now has three levels of priority:
1. **Night Mode** (highest priority) - Full night mode with optional night mode app
2. **Dim Mode** (medium priority) - Dimmed brightness without changing apps
3. **Normal Brightness** (default) - Standard brightness setting

## Implementation Details

### 1. Data Model Changes (`tronbyt_server/models/device.py`)
Added two new fields to the `Device` TypedDict:
```python
dim_time: str  # Time in HH:MM format when dimming should start
dim_brightness: int  # Percentage-based dim brightness (0-100)
```

**Note:** Dim mode automatically ends at `night_end` time (if set) or at 6:00 AM by default.

### 2. Database Logic (`tronbyt_server/db.py`)

#### New Function: `get_dim_mode_is_active(device: Device) -> bool`
- Checks if the current time is within the dim period (dim_time to end time)
- Returns `False` if `dim_time` is not set
- Determines dim end time automatically:
  1. `night_end` time (if set, regardless of whether night mode is enabled)
  2. 6:00 AM (default) if night_end is not set
- Supports overnight dim periods (e.g., 20:00 to 06:00)
- Handles invalid time formats gracefully with logging
- Uses device timezone for accurate time comparison

#### Updated Function: `get_device_brightness_8bit(device: Device) -> int`
Now implements a three-tier priority system:
```python
if get_night_mode_is_active(device):
    return device.get("night_brightness", 1)
elif get_dim_mode_is_active(device):
    return device.get("dim_brightness", device.get("brightness", 50))
else:
    return device.get("brightness", 50)
```

### 3. User Interface (`tronbyt_server/templates/manager/update.html`)

#### Dim Start Time Input Field
- Text input accepting HH:MM or HHMM format
- Pattern validation: `^([0-1]?[0-9]|2[0-3]):?[0-5][0-9]$`
- Placeholder: "20:00 or 2000"
- Can be left empty to disable dim mode
- Help text: "Time to start dimming (e.g., 20:00 or 2000). Dim mode ends at Night End Time (if set) or 6:00 AM by default. Leave empty to disable."

#### Dim Brightness Selector
- Visual button panel with 6 brightness levels (0-5)
- Same UI pattern as normal and night brightness controls
- Default value: 2 (12% brightness)
- JavaScript function `setDimBrightnessUpdate()` handles UI updates

### 4. Form Processing (`tronbyt_server/manager.py`)

#### Time Input Parsing
Uses the existing `parse_time_input()` function which supports:
- HH:MM format (e.g., "20:00", "08:30")
- HHMM format (e.g., "2000", "0830")
- H:MM format (e.g., "8:30")
- HMM format (e.g., "830")
- Single or double digit hours

#### Form Handling in `update()` Route
```python
# Handle dim time and dim brightness
# Note: Dim mode ends at night_end time (if set) or 6:00 AM by default
dim_time = request.form.get("dim_time")
if dim_time and dim_time.strip():
    device["dim_time"] = parse_time_input(dim_time)
elif "dim_time" in device:
    del device["dim_time"]  # Remove if field is empty

# Handle dim brightness
dim_brightness = request.form.get("dim_brightness")
if dim_brightness:
    ui_dim_brightness = int(dim_brightness)
    device["dim_brightness"] = db.ui_scale_to_percent(ui_dim_brightness)
```

#### Display Conversion
Converts stored percentage values to UI scale (0-5) for display:
```python
if "dim_brightness" in ui_device:
    ui_device["dim_brightness"] = db.percent_to_ui_scale(
        ui_device["dim_brightness"]
    )
```

## Usage Examples

### Example 1: Evening Dimming with Night Mode
- **Dim Time**: 20:00 (8:00 PM)
- **Dim Brightness**: 2 (12%)
- **Night Mode Enabled**: Yes
- **Night Start**: 22:00 (10:00 PM)
- **Night End**: 06:00 (6:00 AM)
- **Night Brightness**: 1 (3%)

Timeline:
- Before 20:00: Normal brightness
- 20:00-22:00: Dim brightness (12%)
- 22:00-06:00: Night brightness (3%) + night mode app
- After 06:00: Normal brightness

**Note:** Dim mode automatically ends at night_end (06:00)

### Example 2: Dim Mode Only (No Night Mode)
- **Dim Time**: 19:00 (7:00 PM)
- **Dim Brightness**: 3 (20%)
- **Night Mode Enabled**: No

Timeline:
- Before 19:00: Normal brightness
- 19:00-23:59: Dim brightness (20%)
- 00:00-06:00: Dim brightness (20%) - continues overnight
- After 06:00: Normal brightness

**Note:** Without night mode, dim mode runs until 6:00 AM by default

### Example 3: Overnight Dim Mode with Night Mode
- **Dim Time**: 18:00 (6:00 PM)
- **Dim Brightness**: 2 (12%)
- **Night Mode Enabled**: Yes
- **Night Start**: 22:00 (10:00 PM)
- **Night End**: 08:00 (8:00 AM)
- **Night Brightness**: 1 (3%)

Timeline:
- Before 18:00: Normal brightness
- 18:00-22:00: Dim brightness (12%)
- 22:00-23:59: Night brightness (3%) + night mode app
- 00:00-08:00: Night brightness (3%) + night mode app - continues overnight
- After 08:00: Normal brightness

**Note:** Dim mode would have continued until 08:00, but night mode takes priority from 22:00-08:00

### Example 4: Dim Mode with Night End Set (Night Mode Disabled)
- **Dim Time**: 20:00 (8:00 PM)
- **Dim Brightness**: 2 (12%)
- **Night Mode Enabled**: No
- **Night End**: 07:00 (7:00 AM) - set but night mode not enabled

Timeline:
- Before 20:00: Normal brightness
- 20:00-23:59: Dim brightness (12%)
- 00:00-07:00: Dim brightness (12%) - continues overnight
- After 07:00: Normal brightness

**Note:** Dim mode respects night_end even when night mode is disabled

### Example 5: Disabled Dim Mode
- **Dim Time**: (empty)
- **Night Mode**: Enabled

Timeline:
- Before night start: Normal brightness
- After night start: Night brightness

## Technical Notes

### Time Comparison
- All time comparisons use minutes since midnight for accuracy
- Supports device-specific timezones
- Dim mode DOES support wrapping around midnight (e.g., 20:00 to 06:00)
- If dim end time (night_end or 6:00 AM) is earlier than dim_time, it's treated as next day

### Brightness Scale Conversion
The UI uses a 0-5 scale that maps to percentages:
- 0 → 0%
- 1 → 3%
- 2 → 12%
- 3 → 20%
- 4 → 35%
- 5 → 100%

### Error Handling
- Invalid time formats are logged and ignored (dim mode disabled)
- Missing dim_time field disables dim mode
- Falls back to normal brightness if dim_brightness is not set

## Testing

To test the dim mode feature:

1. Navigate to device settings
2. Set a dim time (e.g., "20:00" or "2000")
3. Set a dim brightness level (e.g., 2)
4. Save the device settings
5. Wait until the dim time or manually adjust system time
6. Check device brightness via `/device_id/brightness` endpoint
7. Verify the brightness matches the dim_brightness setting

## Future Enhancements

Possible improvements:
- Add dim end time (currently dims until midnight or night mode)
- Support multiple dim periods per day
- Add dim mode app (similar to night mode app)
- Add UI indicator showing current mode (normal/dim/night)
- Add schedule preview showing when each mode is active

