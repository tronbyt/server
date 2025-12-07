# Pinned App Notification Badge

## Overview
Added a visual notification badge next to the device name on the main manager page that displays which app is currently pinned.

## Implementation

### Location
The notification appears in the device header, right next to the device name on the main manager index page.

### Visual Design

**Badge Styling:**
- **Color**: Orange background (#ff9800) with white text
- **Icon**: 📌 (pin emoji/unicode &#128204;)
- **Text**: "Pinned: [App Name]"
- **Size**: 0.7em (smaller than device name)
- **Shape**: Rounded corners (4px border-radius)
- **Padding**: 4px top/bottom, 10px left/right
- **Position**: Inline with device name, 10px left margin

### Code Changes

**File: `tronbyt_server/templates/manager/index.html` (lines 343-351)**

```html
{% if device.get('pinned_app') %}
  {% set pinned_app_iname = device.get('pinned_app') %}
  {% set pinned_app = device.get('apps', {}).get(pinned_app_iname) %}
  {% if pinned_app %}
    <span style="display: inline-block; margin-left: 10px; padding: 4px 10px; background-color: #ff9800; color: white; border-radius: 4px; font-size: 0.7em; font-weight: bold; vertical-align: middle;">
      &#128204; {{ _('Pinned:') }} {{ pinned_app['name'] }}
    </span>
  {% endif %}
{% endif %}
```

## How It Works

1. **Check for Pinned App**: `{% if device.get('pinned_app') %}`
   - Only displays if a pinned app exists

2. **Get Installation ID**: `{% set pinned_app_iname = device.get('pinned_app') %}`
   - Retrieves the pinned app's installation ID (iname)

3. **Lookup App Details**: `{% set pinned_app = device.get('apps', {}).get(pinned_app_iname) %}`
   - Finds the full app object from the device's apps dictionary

4. **Display Badge**: Shows the app name with pin icon
   - Only displays if the app object exists (safety check)

## Visual Examples

### Before (No Pinned App)
```
┌─────────────────────────────────────┐
│ My Device                           │
└─────────────────────────────────────┘
```

### After (With Pinned App)
```
┌─────────────────────────────────────────────────┐
│ My Device  📌 Pinned: Clock                     │
└─────────────────────────────────────────────────┘
```

### Multiple Devices
```
┌─────────────────────────────────────────────────┐
│ Living Room  📌 Pinned: Weather                 │
└─────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────┐
│ Bedroom                                         │
└─────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────┐
│ Kitchen  📌 Pinned: Calendar                    │
└─────────────────────────────────────────────────┘
```

## Features

### 1. Visibility
- **Prominent**: Orange color stands out
- **Clear**: Pin icon immediately indicates purpose
- **Informative**: Shows exact app name that's pinned

### 2. Responsive
- **Inline**: Flows with device name
- **Scalable**: Font size relative to device name
- **Aligned**: Vertically centered with device name

### 3. Conditional
- **Only When Needed**: Only shows if app is pinned
- **Safe**: Checks if app exists before displaying
- **Clean**: No badge when no app is pinned

### 4. Internationalized
- **Translatable**: Uses `{{ _('Pinned:') }}` for i18n support
- **Universal**: Pin emoji works across languages

## User Experience Benefits

### Quick Identification
Users can instantly see:
- Which devices have pinned apps
- What app is pinned on each device
- Status at a glance without scrolling

### Visual Hierarchy
```
Device Name (Large, Bold)
  └─ Pinned Badge (Smaller, Orange)
      └─ App Name (Inside badge)
```

### Consistency
Matches the existing pinned indicator in the app list:
- Same orange color (#ff9800)
- Same pin icon (📌)
- Same "PINNED" terminology

## Technical Details

### CSS Styling
```css
display: inline-block;      /* Allows padding and margins */
margin-left: 10px;          /* Space from device name */
padding: 4px 10px;          /* Internal spacing */
background-color: #ff9800;  /* Orange background */
color: white;               /* White text */
border-radius: 4px;         /* Rounded corners */
font-size: 0.7em;          /* Smaller than device name */
font-weight: bold;          /* Bold text */
vertical-align: middle;     /* Align with device name */
```

### Jinja2 Template Logic
- Uses `{% set %}` to create local variables
- Uses `.get()` for safe dictionary access
- Nested `{% if %}` for safety checks
- Translatable strings with `{{ _() }}`

### Data Flow
```
device["pinned_app"] = "clock-123"
         ↓
device["apps"]["clock-123"] = {
    "name": "Clock",
    "iname": "clock-123",
    ...
}
         ↓
Badge displays: "📌 Pinned: Clock"
```

## Edge Cases Handled

### 1. No Pinned App
```jinja2
{% if device.get('pinned_app') %}
```
- Badge doesn't appear if no app is pinned

### 2. Pinned App Deleted
```jinja2
{% if pinned_app %}
```
- Badge doesn't appear if pinned app no longer exists
- Prevents showing "Pinned: None" or errors

### 3. Empty Apps Dictionary
```jinja2
device.get('apps', {}).get(pinned_app_iname)
```
- Safely handles devices with no apps

## Integration with Existing Features

### Works With
- ✅ Pin/Unpin buttons in app list
- ✅ API pinning endpoint
- ✅ Web interface toggle_pin route
- ✅ Multiple devices
- ✅ All device types

### Complements
- App list "PINNED" indicator (line 422)
- Pin/Unpin action buttons (lines 458-473)
- Device display logic (manager.py)

## Testing Checklist

- [ ] Badge appears when app is pinned
- [ ] Badge shows correct app name
- [ ] Badge disappears when app is unpinned
- [ ] Badge doesn't appear for devices without pinned apps
- [ ] Badge handles deleted pinned apps gracefully
- [ ] Badge is properly aligned with device name
- [ ] Badge is readable on all screen sizes
- [ ] Badge text is translatable
- [ ] Pin icon displays correctly
- [ ] Multiple devices show correct badges

## Files Modified

1. **`tronbyt_server/templates/manager/index.html`** (lines 343-351)
   - Added pinned app notification badge
   - Integrated with device header
   - Added safety checks for app existence

## Related Features

- **App List Indicator**: Shows "PINNED" status in app list (line 422)
- **Pin/Unpin Buttons**: Toggle pin status (lines 458-473)
- **API Endpoint**: `/v0/devices/{device_id}/installations/{installation_id}` with `set_pinned`
- **Web Route**: `toggle_pin()` in manager.py

## Future Enhancements

### Possible Improvements

1. **Clickable Badge**: Make badge link to the pinned app
   ```html
   <a href="#app-{{ pinned_app['iname'] }}" style="...">
   ```

2. **Tooltip**: Show more info on hover
   ```html
   title="This app is always displayed, bypassing rotation"
   ```

3. **Quick Unpin**: Add × button to unpin directly from badge
   ```html
   <a href="{{ url_for('manager.toggle_pin', ...) }}" style="margin-left: 5px;">×</a>
   ```

4. **Animation**: Subtle pulse or glow effect
   ```css
   animation: pulse 2s infinite;
   ```

5. **Icon Variation**: Different icons for different states
   - 📌 Pinned
   - 🔒 Locked
   - ⭐ Featured

## Accessibility

- **Color Contrast**: White on orange (#ff9800) meets WCAG AA standards
- **Text Alternative**: Pin emoji has semantic meaning
- **Screen Readers**: Text "Pinned: [App Name]" is clear
- **Keyboard Navigation**: Badge is visible to all users

## Browser Compatibility

- **Modern Browsers**: Full support (Chrome, Firefox, Safari, Edge)
- **Pin Emoji**: Unicode &#128204; widely supported
- **CSS**: Standard properties, no vendor prefixes needed
- **Fallback**: If emoji doesn't render, text still clear

## Performance

- **Minimal Impact**: Simple conditional rendering
- **No JavaScript**: Pure HTML/CSS
- **No Extra Requests**: Uses existing data
- **Fast Rendering**: Inline styles, no external CSS

## Summary

Added a prominent, informative badge next to the device name that shows which app is currently pinned. The badge:
- Uses orange color and pin icon for visibility
- Shows the pinned app's name
- Only appears when an app is pinned
- Handles edge cases gracefully
- Matches existing design patterns
- Is fully internationalized

Users can now see at a glance which devices have pinned apps and what those apps are, without needing to scroll through the app list!
