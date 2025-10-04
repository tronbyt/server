#!/usr/bin/env python3
"""Test script for dim mode functionality."""

# Test the parse_time_input function
print("=" * 60)
print("Testing parse_time_input function")
print("=" * 60)

from tronbyt_server.manager import parse_time_input

test_cases = [
    ("20:00", "20:00"),
    ("2000", "20:00"),
    ("8:30", "08:30"),
    ("830", "08:30"),
]

print("\nTesting parse_time_input:")
for input_val, expected in test_cases:
    try:
        result = parse_time_input(input_val)
        status = "✓" if result == expected else "✗"
        print(
            f'{status} parse_time_input("{input_val}") = "{result}" (expected "{expected}")'
        )
    except Exception as e:
        print(f'✗ parse_time_input("{input_val}") raised {type(e).__name__}: {e}')

print("\n" + "=" * 60)
print("Dim mode feature summary")
print("=" * 60)
print("""
The dim mode feature has been added with the following components:

1. Database Model (models/device.py):
   - dim_time: str - Time in HH:MM format when dimming should start
   - dim_brightness: int - Percentage-based dim brightness (0-100)

2. Logic (db.py):
   - get_dim_mode_is_active(device): Checks if current time >= dim_time
   - get_device_brightness_8bit(device): Updated to use dim_brightness when dim mode is active
   - Priority: night mode > dim mode > normal brightness

3. UI (templates/manager/update.html):
   - Dim Time input field (accepts HH:MM or HHMM format)
   - Dim Brightness selector (0-5 scale, same as other brightness controls)
   - JavaScript function setDimBrightnessUpdate() for UI interaction

4. Form Handling (manager.py):
   - Parses dim_time input using parse_time_input()
   - Converts dim_brightness from UI scale (0-5) to percentage (0-100)
   - Removes dim_time if field is left empty

Usage:
- Set a dim_time (e.g., "20:00") to start dimming at that time
- Set dim_brightness to the desired brightness level during dim mode
- Dim mode is active from dim_time until end of day (or until night mode starts)
- Leave dim_time empty to disable dim mode
- Night mode takes priority over dim mode if both are active
""")
