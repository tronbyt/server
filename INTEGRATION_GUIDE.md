# Integration Guide: Quick Start

## 🚀 Quick Integration (3 Steps)

### Step 1: Add CSS and JS to Your Base Template

Add these lines to your HTML `<head>` (before other CSS files):

```html
<!-- Design System -->
<link rel="stylesheet" href="/static/css/design-system.css">
```

Add before closing `</body>`:

```html
<!-- App Card JavaScript -->
<script src="/static/js/app-card-new.js"></script>
```

### Step 2: Use the New App Card Template

In your Go template where you render app cards, use:

```go
{{ template "app_card" . }}
```

Make sure it points to `/web/templates/partials/app_card_new.html`

### Step 3: Test!

Visit your app page and:
- See compact view by default (horizontal row)
- Click any card to expand to full view
- Click again to collapse
- Press ESC to collapse all cards

**That's it!** ✅

---

## 📁 New Files Created

### CSS & Design Tokens
```
/web/static/css/design-system.css          (717 lines)
```
- 60+ design tokens (colors, typography, spacing, etc.)
- Compact view styles
- Full view styles
- Light/dark mode support
- Mobile responsive breakpoints
- BEM-based class names

### Templates
```
/web/templates/partials/app_card_new.html  (267 lines)
```
- Compact view (default): horizontal row with preview, title, meta, badge, toolbar
- Full view (expanded): large preview, detailed metadata, full action grid
- Supports all existing features: drag & drop, i18n, etc.
- 2:1 aspect ratio for preview images

### JavaScript
```
/web/static/js/app-card-new.js             (107 lines)
```
- `toggleAppCardView(iname, event)` - Main toggle function
- `expandAppCard(iname)` - Programmatically expand a card
- `collapseAppCard(iname)` - Programmatically collapse a card
- `collapseAllAppCards()` - Collapse all expanded cards
- ESC key support

### Documentation
```
/DESIGN_SYSTEM.md          - Complete design token reference
/MIGRATION_GUIDE.md        - Step-by-step migration instructions
/INTEGRATION_GUIDE.md      - This file (quick start)
```

---

## 🎨 Design System Features

### ✅ Compact View (Default)

```
┌─────────────────────────────────────────────────────────────────┐
│ [Preview] Title-123          5 min • 2h ago   [ENABLED]  [▶][✏][👁][📌][📋][🗑] │
│  (80x80)                                                         │
└─────────────────────────────────────────────────────────────────┘
```

- Small preview (80×80px with 2:1 aspect ratio image)
- Title and metadata
- Status badge
- Icon-only toolbar (play, edit, preview, pin, duplicate, delete)
- Click anywhere to expand

### ✅ Full View (Expanded)

```
┌───────────────────────────────────────────────────────────────────┐
│ Title-123  [PINNED]                            [ENABLED/DISABLED] │
│ ⟳ Interval: 5 min  •  🕐 Last rendered: 2h ago (0.554s)           │
├──────────────┬────────────────────────────────────────────────────┤
│              │  [Enable]  [Edit]  [Preview]                       │
│   Preview    │  [Pin]  [Top]  [Bottom]  [Duplicate]  [Copy to▼]  │
│   (256x128)  │  ────────────────────────────────────────────────  │
│              │  [Delete App]                                      │
└──────────────┴────────────────────────────────────────────────────┘
```

- Large preview (256×128px, 2:1 ratio)
- Detailed metadata with icons
- Enable/disable toggle button
- All actions with labels
- Click anywhere to collapse

---

## 🌓 Light & Dark Mode

The design system automatically switches based on OS preference:

**Light Mode**:
- White/off-white backgrounds
- Dark borders and text
- Clean, minimal aesthetic

**Dark Mode**:
- Dark backgrounds (#0a0a0a, #171717)
- Light text (#fafafa)
- Subtle borders (#404040)

No configuration needed - just works!

---

## 📱 Mobile Responsive

### Compact View (Mobile)
- Toolbar wraps to bottom row
- Preview shrinks to 64×64px
- Full width buttons

### Full View (Mobile)
- Preview stacks on top
- Actions below preview
- 3-column grid (instead of 5)
- Larger touch targets

Breakpoint: `768px`

---

## 🎯 Using Design Tokens

You can use the design system tokens in your own components:

```css
/* Custom component using design tokens */
.my-component {
    background-color: var(--surface-base);
    color: var(--text-primary);
    padding: var(--space-4);
    border-radius: var(--radius-md);
    font-family: var(--font-mono);
    border: var(--border-width-thin) solid var(--border-primary);
}
```

**Common Tokens**:

```css
/* Colors */
--bg-primary, --bg-secondary, --bg-tertiary
--text-primary, --text-secondary, --text-tertiary
--accent-orange-500, --accent-red-500

/* Spacing (4px grid) */
--space-1 (4px), --space-2 (8px), --space-3 (12px), --space-4 (16px)

/* Typography */
--font-mono, --font-sans
--font-size-xs, --font-size-sm, --font-size-base
--font-weight-medium, --font-weight-bold

/* Borders & Radius */
--border-primary, --border-secondary
--radius-sm, --radius-md, --radius-lg

/* Effects */
--shadow-md, --transition-base
```

See `DESIGN_SYSTEM.md` for complete reference (60+ tokens).

---

## 🔧 JavaScript API

### Expand/Collapse Functions

```javascript
// Toggle a specific card
toggleAppCardView('app-123', event);

// Programmatically expand
expandAppCard('app-123');

// Programmatically collapse
collapseAppCard('app-123');

// Collapse all expanded cards
collapseAllAppCards();
```

### Keyboard Shortcuts

- **ESC**: Collapse all expanded cards

---

## ✨ Maintained Features

All existing functionality is preserved:

✅ Drag & drop reordering
✅ Enable/disable apps
✅ Pin/unpin functionality
✅ Preview (WebSocket devices)
✅ Edit configuration
✅ Duplicate apps
✅ Copy to other devices
✅ Move to top/bottom
✅ Delete apps
✅ i18n translations
✅ Accessibility (ARIA labels)
✅ Responsive design

---

## 🗂 Code Organization

### BEM Naming Convention

```html
<!-- Block: Component root -->
<div class="app-card">

    <!-- Element: Child of block -->
    <div class="app-card__preview">
        <img class="app-card__preview-image">
    </div>

    <!-- Modifier: Variant -->
    <span class="app-card__badge app-card__badge--enabled">

    <!-- State: Dynamic state -->
    <button class="app-card__action-btn is-enabled">

</div>
```

**Pattern**:
- `.block` - Root component
- `.block__element` - Child (uses `__`)
- `.block__element--modifier` - Variant (uses `--`)
- `.is-state` - JavaScript state (uses `.is-`)

---

## 🚨 Troubleshooting

### Card won't expand
→ Check that `app-card-new.js` is loaded. Look for console errors.

### Styles look wrong
→ Ensure `design-system.css` loads **before** other CSS files.

### Dark mode not working
→ Change your **OS theme**, not just browser. Uses `prefers-color-scheme`.

### Preview image is cropped
→ Make sure you're using `app_card_new.html`, not old `app_card.html`.

---

## 📊 File Structure

```
/web
├── static/
│   ├── css/
│   │   ├── design-system.css          ← NEW: Design tokens + components
│   │   └── manager.css                 ← OLD: Marked with deprecation comments
│   └── js/
│       └── app-card-new.js             ← NEW: Expand/collapse logic
└── templates/
    └── partials/
        ├── app_card.html               ← OLD: Marked for cleanup
        └── app_card_new.html           ← NEW: Compact + Full views

/tmp/
├── dark-design-example.html            ← Reference design (dark mode)
└── light-design-example.html           ← Reference design (light mode)

/
├── DESIGN_SYSTEM.md                    ← Complete token reference
├── MIGRATION_GUIDE.md                  ← Step-by-step migration
└── INTEGRATION_GUIDE.md                ← This file (quick start)
```

---

## 🎓 Next Steps

1. **Integrate** - Follow Step 1-3 above
2. **Test** - Check compact/full views, mobile, dark mode
3. **Extend** - Use design tokens for other components
4. **Clean up** - Remove old code after testing (see MIGRATION_GUIDE.md)

---

## 📚 Additional Resources

- **Design Token Reference**: See `DESIGN_SYSTEM.md`
- **Migration Guide**: See `MIGRATION_GUIDE.md`
- **Example Designs**:
  - `/tmp/dark-design-example.html`
  - `/tmp/light-design-example.html`

---

**Questions?** Check the documentation or create a GitHub issue.
