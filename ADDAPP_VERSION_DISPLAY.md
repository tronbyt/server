# Add App Page - System Apps Version Display

## Overview
Added display of system-apps version (commit hash) next to the "System Apps" title on the add app page, with a clickable link to the GitHub repository at that specific commit.

## Changes Made

### 1. Manager Route (`tronbyt_server/manager.py`)

Updated the `addapp` route to pass `system_repo_info` to the template:

```python
# Sort apps_list so that installed apps appear first
apps_list.sort(key=lambda app_metadata: not app_metadata["is_installed"])

system_repo_info = system_apps.get_system_repo_info(db.get_data_dir())

return render_template(
    "manager/addapp.html",
    device=g.user["devices"][device_id],
    apps_list=apps_list,
    custom_apps_list=custom_apps_list,
    system_repo_info=system_repo_info,
)
```

### 2. Add App Template (`tronbyt_server/templates/manager/addapp.html`)

#### Updated the `render_app_list` Macro

Added `show_version` parameter to the macro and inline version display:

```html
{% macro render_app_list(title, search_id, grid_id, app_list, show_controls=true, show_version=false) %}
<div class="app-group">
  <h3 style="display: inline-block; margin-right: 10px;">{{ title }}</h3>
  {% if show_version and system_repo_info and system_repo_info.commit_hash %}
  <span style="font-size: 14px; color: #888;">
    (version: <a href="{{ system_repo_info.commit_url }}" target="_blank" style="color: #4CAF50; text-decoration: none;">{{ system_repo_info.commit_hash }}</a>)
  </span>
  {% endif %}
  ...
```

#### Updated System Apps Call

Enabled version display for System Apps:

```html
{{ render_app_list(_('System Apps'), 'system_search', 'system_app_grid', apps_list, show_version=true) }}
```

## Features

### Display Information
- **Inline with Title**: Version appears on the same line as "System Apps" heading
- **Commit Hash**: Shows the short commit hash (7 characters)
- **Clickable Link**: Links to the exact commit on GitHub
- **Subtle Styling**: Gray text with green link to not distract from main content

### Visual Design
- Inline display: `System Apps (version: abc1234)`
- H3 heading uses `display: inline-block` to allow inline version text
- Version text is smaller (14px) and gray (#888)
- Commit hash link is green (#4CAF50) matching site theme
- Opens in new tab (`target="_blank"`)

### Conditional Display
- Only shows if `show_version=true` is passed to macro
- Only shows if `system_repo_info` exists
- Only shows if `commit_hash` is available
- Custom Apps section does NOT show version (only System Apps)

## User Experience

### On Add App Page

Users will see:

```
Custom Apps
[app grid...]

─────────────────────────────────

System Apps (version: abc1234)
                      ↑ clickable link to GitHub
[search and filter controls]
[app grid...]
```

### Example Display

```
System Apps (version: a1b2c3d)
```

Where "a1b2c3d" is a clickable link to:
`https://github.com/tronbyt/apps/tree/a1b2c3d1234567890abcdef`

## Benefits

1. **Visibility**: Users can see which version of system apps is available
2. **Debugging**: Easy to verify which commit is deployed when reporting issues
3. **Traceability**: Direct link to view the exact code on GitHub
4. **Non-intrusive**: Subtle styling doesn't distract from app selection
5. **Consistent**: Matches the pattern used on firmware and admin pages
6. **Selective**: Only shows for System Apps, not Custom Apps (which are user-specific)

## Technical Notes

- The version is only displayed if the commit hash is available
- If the system-apps directory is not a git repository, the version won't appear
- The commit URL format is: `{repo_url}/tree/{commit_hash}`
- Uses inline-block display to keep title and version on same line
- Fully internationalized with `{{ _() }}` translation markers for "System Apps"
- The macro is reusable - can be enabled for other app lists if needed

## Files Modified

1. `tronbyt_server/manager.py` - Added system_repo_info to addapp route
2. `tronbyt_server/templates/manager/addapp.html` - Added version display to macro and enabled for System Apps

## Testing

To test the feature:

1. Navigate to any device's "Add App" page
2. Scroll to the "System Apps" section
3. Verify the version appears next to the title: `System Apps (version: abc1234)`
4. Click the commit hash link - should open GitHub at that specific commit
5. Verify Custom Apps section does NOT show a version
6. Verify the display is inline and doesn't break the layout

## Comparison with Other Pages

### Admin Settings Page
- Shows detailed box with commit, repo URL, and branch
- Includes management buttons
- More prominent display

### Firmware Generation Page
- Shows version in a styled info box
- Includes management button for admins
- Separate box similar to firmware version

### Add App Page (This Implementation)
- Shows version inline with title
- Subtle, non-intrusive display
- No management buttons (not needed in this context)
- Focuses on quick reference

## Future Enhancements

Possible future improvements:
- Add tooltip showing full commit hash on hover
- Show commit date
- Add "last updated" timestamp
- Include branch name if not on main
- Add visual indicator if version is outdated

