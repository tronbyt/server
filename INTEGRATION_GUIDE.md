# Integration Guide: Teenage Engineering-Inspired Design System

## 🎯 Design Philosophy

**Inspiration**: Teenage Engineering  
**Aesthetic**: Sharp & Clean, Instrument-Like Precision

### Core Principles

1. **Sharp & Clean**: No rounded corners—just crisp, rectangular borders
2. **Focused Palette**: High-contrast, monochrome palette with monospace fonts
3. **Instrument Feel**: Precise, functional, focused on content
4. **Mobile Friendly**: Natural stacking layout for touch interfaces

---

## 🚀 Quick Integration (3 Steps)

### Step 1: Add CSS to Your Base Template

Add to your HTML `<head>` (before other CSS files):

```html
<!-- Teenage Engineering-Inspired Design System -->
<link rel="stylesheet" href="/static/css/design-system.css">
```

### Step 2: Use the App Card Template

In your Go template where you render app cards:

```go
{{ template "app_card" . }}
```

Points to: `/web/templates/partials/app_card.html`

### Step 3: Test!

Visit your app page and verify:
- Compact view by default (horizontal row)
- Click any card to expand to full view
- Sharp, rectangular borders (no rounded corners)
- Monospace typography throughout
- Mobile: Actions stack naturally below content

**That's it!** ✅

---

## 🎨 Design Language

### Visual Identity

```
Sharp Edges          Monospace Type       High Contrast
┌────────┐          ENABLED              ■■■□□□
│        │          DISABLED             ███░░░
│  CARD  │          Title-123            Black/White
│        │          5min • 2h ago        Orange Accent
└────────┘          
```

### Typography

- **Font Family**: `ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas`
- **Style**: All caps for labels (ENABLED, DISABLED, PINNED)
- **Hierarchy**: Bold weights for titles, regular for metadata

### Color Palette

**Light Mode**:
- Background: `#ffffff` (white)
- Text: `#171717` (near-black)
- Borders: `#404040` (dark gray)
- Accent: `#f97316` (orange)

**Dark Mode**:
- Background: `#0a0a0a` (near-black)
- Text: `#fafafa` (off-white)
- Borders: `#404040` (medium gray)
- Accent: `#ea580c` (darker orange)

### Borders & Corners

- **Border Radius**: `0` (sharp, rectangular)
- **Border Width**: `1px` (crisp lines)
- **Border Style**: `solid` (clean, no dashed/dotted)

---

## 📱 Mobile-First Approach

### Desktop Layout
```
┌──────────────────────────────────────────────────┐
│ [Thumb] Title-123  5min•2h  [ENABLED] [▶][✏][📌] │
└──────────────────────────────────────────────────┘
```

### Mobile Layout (Stacked)
```
┌──────────────────────────┐
│ [Thumb] Title-123        │
│         5min • 2h ago    │
│         [ENABLED]        │
├──────────────────────────┤
│ [▶] [✏] [📌] [📋] [🗑]  │
└──────────────────────────┘
```

**Why Stacking?**
- Better touch targets (no cramped horizontal space)
- Natural reading flow (top to bottom)
- Easier one-handed operation
- Clearer visual hierarchy

---

## 🏗 Component Structure

### Compact View (Default)

```html
<div class="card-container hover-bg compact-view">
  <div class="compact-row">
    <!-- Preview Thumbnail -->
    <div class="thumb-box compact">
      <img class="thumb-image" src="...">
    </div>
    
    <!-- Info Section -->
    <div class="compact-info">
      <h3>Title-123</h3>
      <div>5 min • 2h ago</div>
    </div>
    
    <!-- Status Badge -->
    <div class="compact-status">
      <div class="badge is-enabled">ENABLED</div>
    </div>
    
    <!-- Actions Toolbar -->
    <div class="compact-actions">
      <button class="btn-tool">▶</button>
      <button class="btn-tool">✏</button>
      <!-- ... -->
    </div>
  </div>
</div>
```

### Full View (Expanded)

```html
<div class="card-container full-view">
  <!-- Header with metadata -->
  <div class="card-header border-b">
    <h2 class="card-title">Title-123</h2>
    <div class="header-meta-row">
      Interval: 5 min • Last rendered: 2h ago
    </div>
  </div>
  
  <!-- Content: Preview + Actions -->
  <div class="view-content-wrapper">
    <div class="preview-area">
      <div class="led-panel">
        <img class="preview-image" src="...">
      </div>
    </div>
    
    <div class="actions-area">
      <!-- Primary actions (3-column grid) -->
      <div class="grid-3">
        <button class="btn-action-lg">Enable</button>
        <button class="btn-action-lg">Edit</button>
        <button class="btn-action-lg">Preview</button>
      </div>
      
      <!-- Secondary actions (5-column grid) -->
      <div class="grid-5">
        <button class="btn-action-sm">Pin</button>
        <button class="btn-action-sm">Top</button>
        <!-- ... -->
      </div>
    </div>
  </div>
</div>
```

---

## 🎯 Key CSS Classes

### Layout Classes

| Class | Purpose |
|-------|---------|
| `.card-container` | Main card wrapper (sharp borders, no radius) |
| `.compact-row` | Horizontal layout for compact view |
| `.compact-info` | Title and metadata section |
| `.compact-status` | Status badge container |
| `.compact-actions` | Toolbar with icon buttons |

### Button Classes

| Class | Purpose |
|-------|---------|
| `.btn-tool` | Compact toolbar button (icon-only) |
| `.btn-action-lg` | Large action button (icon + label) |
| `.btn-action-sm` | Small action button (icon + label) |
| `.btn-delete` | Delete button (red accent) |

### State Classes

| Class | Purpose |
|-------|---------|
| `.is-enabled` | Orange accent for enabled state |
| `.is-pinned` | Black/white invert for pinned state |
| `.hidden` | Hide element |

---

## 🌓 Light & Dark Mode

Automatic switching via `prefers-color-scheme`:

```css
/* Light Mode (Default) */
:root {
  --white: #ffffff;
  --black: #000000;
  --neutral-900: #171717;
}

/* Dark Mode */
@media (prefers-color-scheme: dark) {
  :root {
    --white: #171717;
    --black: #000000;
    --neutral-900: #fafafa;
  }
}
```

**No JavaScript required** - pure CSS media queries.

---

## 📐 Design Tokens

### Spacing (No Grid System)

Direct padding/margin values:
- `0.5rem` (8px)
- `0.75rem` (12px)
- `1rem` (16px)
- `1.5rem` (24px)

### Typography Scale

```css
--font-mono: ui-monospace, SFMono-Regular, ...;

/* Sizes */
font-size: 0.75rem;   /* 12px - Small labels */
font-size: 0.875rem;  /* 14px - Body text */
font-size: 1.5rem;    /* 24px - Card titles */
```

### Color Variables

```css
/* Neutrals */
--neutral-50, --neutral-100, --neutral-200
--neutral-300, --neutral-400, --neutral-500
--neutral-600, --neutral-700, --neutral-900

/* Accents */
--orange-500: #f97316;  /* Primary accent */
--orange-600: #ea580c;  /* Hover state */
--red-500: #ef4444;     /* Delete action */
--red-600: #dc2626;     /* Delete hover */
```

---

## ✨ Maintained Features

All existing functionality preserved:

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

## 🚨 Troubleshooting

### Rounded corners appearing
→ Check for conflicting CSS. Design system uses `border-radius: 0 !important`.

### Wrong font family
→ Ensure `design-system.css` loads before other CSS files.

### Mobile layout not stacking
→ Test at `<640px` width. Use browser DevTools responsive mode.

### Dark mode not switching
→ Change **OS theme** (System Preferences), not browser theme.

---

## 📚 File Reference

```
/web/static/css/design-system.css
  - Sharp borders (no border-radius)
  - Monospace typography
  - Mobile stacking layout (@media max-width: 640px)
  - Light/dark mode support

/web/templates/partials/app_card.html
  - Compact view (horizontal on desktop)
  - Full view (expandable)
  - Mobile-friendly stacking
```

---

## 🎓 Next Steps

1. **Review** the migrated `app_card.html` component
2. **Apply** the design language to other templates
3. **Follow** `MIGRATION_GUIDE.md` for step-by-step instructions
4. **Reference** this guide when creating new components

---

**Design Inspiration**: Teenage Engineering  
**Focus**: Content over decoration, precision over flair
