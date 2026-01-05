# Migration Guide: New Design System

## Overview

This guide explains how to migrate from the old app card design to the new systematic design.

---

## What's New?

### 1. **New Design System**
- **File**: `/web/static/css/design-system.css`
- **Features**:
  - 60+ design tokens for colors, typography, spacing, etc.
  - Automatic light/dark mode support
  - BEM naming convention
  - Framework-free (no W3.CSS dependency)
  - Fully responsive

### 2. **Redesigned App Card**
- **File**: `/web/templates/partials/app_card_new.html`
- **Features**:
  - **Compact View (Default)**: Horizontal row with small preview, metadata, status badge, and action toolbar
  - **Full View (Expanded)**: Click/tap to expand - shows large preview, all metadata, and full action grid
  - Maintains all existing functionality (drag & drop, i18n, etc.)
  - 2:1 aspect ratio for all preview images (no cropping)

### 3. **JavaScript Module**
- **File**: `/web/static/js/app-card-new.js`
- **Features**:
  - `toggleAppCardView(iname, event)` - Toggle between compact/full
  - `expandAppCard(iname)` - Expand to full view
  - `collapseAppCard(iname)` - Collapse to compact
  - `collapseAllAppCards()` - Collapse all cards
  - ESC key support (press ESC to collapse all)

---

## Migration Steps

### Step 1: Include the New Design System

Add to your base HTML template `<head>`:

```html
<!-- NEW: Design System CSS -->
<link rel="stylesheet" href="/static/css/design-system.css">

<!-- NEW: App Card JavaScript -->
<script src="/static/js/app-card-new.js"></script>
```

**Important**: Load `design-system.css` **before** any old CSS files to avoid conflicts.

### Step 2: Update Template References

Find where you're using the app card template:

**Old**:
```go
{{ template "app_card" . }}
```

**New**:
Change the template file from `app_card.html` to `app_card_new.html`:

1. Rename `/web/templates/partials/app_card_new.html` to `/web/templates/partials/app_card.html`, OR
2. Update your template include to point to the new file

### Step 3: Update JavaScript Event Handlers

The new design system maintains all existing JavaScript function calls:
- `toggleEnabled(deviceID, iname)` ✅
- `togglePin(deviceID, iname)` ✅
- `previewApp(deviceID, iname, null, button)` ✅
- `duplicateApp(deviceID, iname)` ✅
- `deleteApp(deviceID, iname, false, message)` ✅
- `moveApp(deviceID, iname, direction)` ✅
- `duplicateAppToDevice(...)` ✅

**No changes required** to existing JavaScript handlers.

### Step 4: Test Both Modes

1. **Light Mode**:
   - Set system preference to light mode
   - Verify colors, borders, and text are readable

2. **Dark Mode**:
   - Set system preference to dark mode
   - Verify automatic color switching works

3. **Mobile Responsiveness**:
   - Test on mobile (< 768px width)
   - Compact view: Actions should wrap to bottom
   - Full view: Preview should stack on top, actions below

4. **Expand/Collapse**:
   - Click card to expand to full view
   - Click expanded card to collapse back to compact
   - Press ESC to collapse all expanded cards

### Step 5: Remove Old Code (After Testing)

Once you've confirmed the new design works:

1. **Remove old CSS**:
   - Delete the "OLD DESIGN" section in `/web/static/css/manager.css` (lines 280-1212)

2. **Remove old template**:
   - Delete the old `/web/templates/partials/app_card.html` (or remove the template definition)

3. **Clean up W3.CSS dependencies** (if not used elsewhere):
   - Remove `<link>` tag for `/static/css/w3.css`

---

## Design Token Reference

### Using Design Tokens in Your Own Components

The design system tokens can be used throughout your application:

```css
/* Example: Custom button */
.my-button {
    background-color: var(--accent-orange-500);
    color: var(--text-inverse);
    padding: var(--space-3) var(--space-4);
    border-radius: var(--radius-md);
    font-family: var(--font-mono);
    font-size: var(--font-size-sm);
    transition: background-color var(--transition-base);
}

.my-button:hover {
    background-color: var(--accent-orange-600);
}
```

### Common Token Categories

| Category | Example Tokens |
|----------|----------------|
| **Colors** | `--bg-primary`, `--text-secondary`, `--accent-orange-500` |
| **Spacing** | `--space-1` (4px), `--space-4` (16px), `--space-8` (32px) |
| **Typography** | `--font-mono`, `--font-size-sm`, `--font-weight-bold` |
| **Borders** | `--border-primary`, `--radius-md`, `--border-width-thin` |
| **Effects** | `--shadow-md`, `--transition-base` |

See `DESIGN_SYSTEM.md` for complete reference.

---

## BEM Naming Convention

The new system uses BEM (Block Element Modifier) for consistent class names:

```html
<!-- Block -->
<div class="app-card">

    <!-- Element (child of block) -->
    <div class="app-card__preview">
        <img class="app-card__preview-image">
    </div>

    <!-- Element with modifier (variant) -->
    <span class="app-card__badge app-card__badge--enabled">ENABLED</span>

    <!-- State class -->
    <button class="app-card__action-btn is-pinned">Pin</button>

</div>
```

**Pattern**:
- `.block` - Component root
- `.block__element` - Child element (uses `__`)
- `.block__element--modifier` - Variant (uses `--`)
- `.is-state` - Dynamic state (uses `.is-`)

---

## Expanding the Design System

### Creating New Components

When building new components, follow this pattern:

1. **Define tokens first** (if new values needed)
2. **Use BEM naming**
3. **Support light/dark modes**
4. **Make it responsive**
5. **Document in DESIGN_SYSTEM.md**

Example:

```css
/* New component: Alert */
.alert {
    background-color: var(--surface-base);
    border: var(--border-width-thin) solid var(--border-primary);
    padding: var(--space-4);
    border-radius: var(--radius-md);
    font-family: var(--font-mono);
}

.alert__icon {
    width: 16px;
    height: 16px;
    color: var(--text-secondary);
}

.alert__title {
    font-size: var(--font-size-sm);
    font-weight: var(--font-weight-bold);
    color: var(--text-primary);
}

.alert--error {
    border-color: var(--accent-red-500);
}
```

---

## Troubleshooting

### Issue: Styles not applying

**Solution**: Ensure `design-system.css` loads **before** old CSS files. Check browser DevTools to verify CSS is loaded.

### Issue: Card won't expand/collapse

**Solution**: Verify `app-card-new.js` is loaded. Check console for JavaScript errors.

### Issue: Dark mode not working

**Solution**: The design system uses `prefers-color-scheme` media query. Set your OS to dark mode, not just browser theme.

### Issue: Preview image is cropped

**Solution**: Ensure you're using the new template (`app_card_new.html`). Old template may still be in use.

### Issue: Buttons overlapping on mobile

**Solution**: Clear browser cache. Old CSS may be cached.

---

## Support and Questions

For issues or questions:
1. Check `DESIGN_SYSTEM.md` for design token reference
2. Review the example files:
   - `/tmp/dark-design-example.html`
   - `/tmp/light-design-example.html`
3. Report bugs via GitHub issues

---

## Version History

- **v1.0.0** (2026-01-03): Initial release
  - Design token system
  - Compact + Full view app cards
  - Light/dark mode support
  - Mobile responsive
  - BEM naming convention
