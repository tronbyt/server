# Interstitial App Feature

## Overview

This PR introduces the **Interstitial App** feature, which allows users to display a designated app between each regular app in their device rotation. This feature provides enhanced flexibility for displaying important information or branding consistently across the app rotation cycle.

## Features

### Core Functionality
- **Interstitial App Selection**: Users can designate any installed app as an interstitial app
- **Enable/Disable Toggle**: Interstitial apps can be easily enabled or disabled without removing the configuration
- **Automatic Insertion**: Interstitial apps are automatically inserted between each regular app in the rotation
- **Priority Display**: Interstitial apps display regardless of their enabled/schedule status in the regular rotation
- **Backward Compatibility**: Existing app indices are preserved and handled gracefully

### User Interface
- **Device Settings Integration**: Interstitial app configuration is available in the device settings page
- **App Selection Dropdown**: Easy selection of interstitial app from installed apps
- **Clear Labeling**: Descriptive labels and help text explain the feature functionality
- **Visual Feedback**: Settings are clearly displayed and persist across sessions

## Technical Implementation

### Data Model Changes
- Added `interstitial_enabled: bool = False` to Device model
- Added `interstitial_app: str | None = None` to Device model (stores app iname)

### App Selection Logic
The interstitial app feature integrates seamlessly with the existing app selection logic:

```python
# Create expanded apps list with interstitial apps inserted
expanded_apps_list = []
interstitial_app_iname = device.interstitial_app
interstitial_enabled = device.interstitial_enabled

for i, regular_app in enumerate(apps_list):
    # Add the regular app
    expanded_apps_list.append(regular_app)
    
    # Add interstitial app after each regular app (except the last one)
    if (interstitial_enabled 
        and interstitial_app_iname 
        and interstitial_app_iname in device.apps 
        and i < len(apps_list) - 1):
        interstitial_app = device.apps[interstitial_app_iname]
        expanded_apps_list.append(interstitial_app)
```

### Priority System
Interstitial apps follow the established priority system:
1. **Pinned Apps** (highest priority)
2. **Night Mode Apps**
3. **Interstitial Apps** (when enabled and valid)
4. **Regular Apps** (normal rotation)

### Compatibility Handling
- **Index Management**: Existing app indices are preserved and handled gracefully
- **Migration Support**: Old indices are automatically adjusted for the expanded app list
- **Fallback Logic**: Invalid indices are reset to prevent errors

## Files Modified

### Core Models
- `tronbyt_server/models/device.py` - Added interstitial app fields to Device model

### Router Logic
- `tronbyt_server/routers/manager.py` - Updated app selection logic in `_next_app_logic()` and `/currentapp` endpoint

### User Interface
- `tronbyt_server/templates/manager/update.html` - Added interstitial app configuration UI

### Form Handling
- `tronbyt_server/routers/manager.py` - Added form processing for interstitial app settings

## Usage Examples

### Basic Configuration
1. Navigate to device settings
2. Enable "Interstitial App" checkbox
3. Select desired app from dropdown
4. Save settings

### App Rotation Pattern
With interstitial apps enabled, the rotation pattern becomes:
```
Regular App 1 → Interstitial App → Regular App 2 → Interstitial App → Regular App 3
```

### Priority Behavior
- If an app is pinned, it displays continuously (no interstitial)
- If night mode is active, night mode app displays (no interstitial)
- If interstitial is enabled and valid, it displays between regular apps
- Regular apps follow normal rotation and scheduling rules

## Benefits

### For Users
- **Enhanced Visibility**: Important information can be displayed more frequently
- **Branding Opportunities**: Company logos or announcements can be shown between content
- **Flexible Scheduling**: Interstitial apps bypass normal scheduling constraints
- **Easy Management**: Simple enable/disable without losing configuration

### For Developers
- **Clean Integration**: Seamlessly integrates with existing app selection logic
- **Maintainable Code**: Follows established patterns and conventions
- **Backward Compatible**: No breaking changes to existing functionality
- **Extensible Design**: Foundation for future rotation enhancements

## Testing Considerations

### Manual Testing
- [ ] Enable interstitial app and verify it displays between regular apps
- [ ] Disable interstitial app and verify normal rotation resumes
- [ ] Test with pinned apps to ensure interstitial doesn't interfere
- [ ] Test with night mode to verify priority system works correctly
- [ ] Verify interstitial app displays even when disabled in regular rotation
- [ ] Test app index handling with various app counts

### Edge Cases
- [ ] Device with no apps (should handle gracefully)
- [ ] Device with only one app (interstitial should not display)
- [ ] Interstitial app gets deleted (should handle gracefully)
- [ ] Invalid interstitial app selection (should reset to None)

## Migration Notes

### Database Changes
- No database schema changes required
- New fields use default values for existing devices
- Existing app indices are preserved and handled gracefully

### Backward Compatibility
- Existing devices continue to work without changes
- Old app indices are automatically adjusted
- No data loss or corruption risks

## Future Enhancements

### Potential Improvements
- **Multiple Interstitial Apps**: Support for rotating between multiple interstitial apps
- **Conditional Display**: Show interstitial apps only at certain times
- **Frequency Control**: Control how often interstitial apps appear
- **App-Specific Interstitials**: Different interstitial apps for different regular apps

### API Extensions
- REST API endpoints for interstitial app management
- WebSocket notifications for interstitial app changes
- Bulk operations for managing interstitial apps across devices

## Conclusion

The Interstitial App feature provides a powerful and flexible way to enhance the app rotation experience. It integrates seamlessly with existing functionality while providing new capabilities for users to customize their device display patterns. The implementation follows established patterns and maintains full backward compatibility.

This feature opens up new possibilities for content display strategies and provides a foundation for future rotation enhancements.
