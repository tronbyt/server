# Uploaded App Delete Button Enhancement

## Overview
Enhanced the delete button for uploaded apps on the add app page to make it more prominent and user-friendly with better styling and confirmation dialog.

## Changes Made

### 1. Template Updates (`tronbyt_server/templates/manager/addapp.html`)

#### Enhanced Delete Link
Added styling, icon, and confirmation dialog:

```html
{% if is_custom_apps %}
<a href="{{ url_for('manager.deleteupload', filename=app['path'].split('/')[-1], device_id=device['id']) }}"
   class="delete-upload-btn"
   onclick="event.stopPropagation(); return confirm('{{ _('Delete this uploaded app?') }}');">
  🗑️ {{ _('Delete') }}
</a>
{% endif %}
```

#### Updated Macro Signature
Added `is_custom_apps` parameter to the `render_app_list` macro:

```html
{% macro render_app_list(title, search_id, grid_id, app_list, show_controls=true, show_version=false, is_custom_apps=false) %}
```

#### Updated Macro Calls
Pass `is_custom_apps=true` for custom apps and `is_custom_apps=false` for system apps:

```html
{{ render_app_list(_('Custom Apps'), 'custom_search', 'custom_app_grid', custom_apps_list, show_controls=false, is_custom_apps=true) }}

{{ render_app_list(_('System Apps'), 'system_search', 'system_app_grid', apps_list, show_version=true, is_custom_apps=false) }}
```

#### Added CSS Styling
New styles for the delete button:

```css
.delete-upload-btn {
  display: inline-block;
  background-color: #f44336;
  color: white;
  padding: 5px 10px;
  border-radius: 3px;
  text-decoration: none;
  font-size: 12px;
  font-weight: bold;
  margin-top: 8px;
  transition: background-color 0.2s;
}

.delete-upload-btn:hover {
  background-color: #d32f2f;
  text-decoration: none;
}
```

## Features

### Visual Improvements
- **Red Button**: Prominent red background (#f44336) to indicate destructive action
- **Trash Icon**: 🗑️ emoji for visual clarity
- **Bold Text**: Makes the button stand out
- **Rounded Corners**: Modern, polished appearance
- **Hover Effect**: Darker red (#d32f2f) on hover for feedback

### Functional Improvements
- **Confirmation Dialog**: Asks "Delete this uploaded app?" before deletion
- **Event Propagation Stop**: `event.stopPropagation()` prevents app selection when clicking delete
- **Internationalized**: Uses `{{ _() }}` for translation support

### User Experience
- **Clear Intent**: Red color and trash icon clearly indicate deletion
- **Safety**: Confirmation dialog prevents accidental deletion
- **No Interference**: Clicking delete doesn't select the app
- **Responsive**: Hover effect provides visual feedback

## Before vs After

### Before
```
[App Preview]
App Name - Description
From Author
Delete  ← plain text link
```

### After
```
[App Preview]
App Name - Description
From Author
┌─────────────┐
│ 🗑️ Delete  │  ← red button with icon
└─────────────┘
```

## Technical Details

### Conditional Display
The delete button only appears for uploaded apps:
```python
{% if is_custom_apps %}
```

This uses a flag passed to the macro that explicitly indicates whether the app list contains custom/uploaded apps (not system apps). This is more reliable than checking the path string.

### Event Handling
```javascript
onclick="event.stopPropagation(); return confirm('...');"
```

1. `event.stopPropagation()` - Prevents the click from bubbling up to the app-item div (which would select the app)
2. `return confirm('...')` - Shows confirmation dialog; only proceeds if user clicks OK

### Existing Route
The delete functionality uses the existing `deleteupload` route:
```python
url_for('manager.deleteupload', filename=app['path'].split('/')[-1], device_id=device['id'])
```

## User Flow

1. User navigates to Add App page
2. User sees uploaded apps in the Custom Apps section
3. Each uploaded app shows a red "🗑️ Delete" button
4. User clicks the delete button
5. Confirmation dialog appears: "Delete this uploaded app?"
6. If user clicks OK:
   - App is deleted from the server
   - Page redirects back to Add App page
   - App no longer appears in the list
7. If user clicks Cancel:
   - Nothing happens
   - App remains in the list

## Benefits

1. **Visibility**: Red button is much more noticeable than plain text link
2. **Safety**: Confirmation dialog prevents accidental deletion
3. **Clarity**: Trash icon makes the action obvious
4. **Professional**: Styled button looks polished and modern
5. **Consistent**: Matches the styling of other action buttons in the app
6. **Accessible**: Clear visual and textual indication of purpose

## Files Modified

1. `tronbyt_server/templates/manager/addapp.html`
   - Enhanced delete link with class, icon, and confirmation
   - Added CSS styling for delete button

## Testing

To test the feature:

1. Upload a .star file using the "Upload .star file" button
2. Navigate to the Add App page
3. Find the uploaded app in the Custom Apps section
4. Verify the red "🗑️ Delete" button appears
5. Click the delete button
6. Verify confirmation dialog appears
7. Click Cancel - verify nothing happens
8. Click the delete button again
9. Click OK - verify app is deleted and page refreshes
10. Verify the app no longer appears in the list

## Edge Cases Handled

- **Click Propagation**: Delete button click doesn't select the app
- **Confirmation**: User must confirm before deletion
- **System Apps**: Delete button only shows for uploaded apps, not system apps
- **Hover State**: Visual feedback when hovering over button
- **Text Decoration**: No underline on hover (common link issue)

## Future Enhancements

Possible future improvements:
- Add undo functionality
- Show toast notification after deletion
- Add bulk delete option
- Show file size next to delete button
- Add "last uploaded" date display
- Implement soft delete with recovery option
