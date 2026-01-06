# Migration Guide: Teenage Engineering-Inspired Design System

## Overview

This guide shows you how to apply the **Teenage Engineering-inspired design language** to other templates, screens, and components in the Tronbyt Server project.

**Reference Implementation**: `app_card.html` (completed migration)

---

## 🎯 Design Principles

### 1. Sharp & Clean
- **No rounded corners**: `border-radius: 0 !important`
- **Crisp borders**: `border: 1px solid var(--neutral-700)`
- **Rectangular shapes**: All containers are sharp-edged boxes

### 2. Focused Palette
- **Monochrome base**: Black, white, and grays
- **High contrast**: `#171717` on `#ffffff` (light), `#fafafa` on `#0a0a0a` (dark)
- **Single accent**: Orange (`#f97316`) for active/enabled states
- **Monospace typography**: `ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas`

### 3. Instrument Feel
- **All-caps labels**: `ENABLED`, `DISABLED`, `PINNED`
- **Precise spacing**: Direct rem values (0.5rem, 0.75rem, 1rem, 1.5rem)
- **Functional focus**: Content over decoration
- **Clear hierarchy**: Bold titles, regular metadata

### 4. Mobile Friendly
- **Natural stacking**: Vertical layout on mobile (`@media max-width: 640px`)
- **Better touch targets**: Full-width buttons, adequate padding
- **One-handed operation**: Actions at bottom, easy to reach
- **Clear flow**: Top to bottom reading order

---

## 📋 Migration Checklist

Use this checklist when migrating any template/component:

### Visual Style
- [ ] Remove all `border-radius` (or set to `0`)
- [ ] Use monospace font (`var(--font-mono)`)
- [ ] Apply sharp borders (`1px solid`)
- [ ] Use high-contrast colors (neutrals + orange accent)
- [ ] Convert labels to ALL CAPS where appropriate

### Layout
- [ ] Desktop: Horizontal/grid layout
- [ ] Mobile: Vertical stacking (`@media max-width: 640px`)
- [ ] Use CSS Grid or Flexbox (no floats)
- [ ] Adequate spacing (0.75rem minimum for touch targets)

### Typography
- [ ] Font family: `var(--font-mono)`
- [ ] Font sizes: `0.75rem`, `0.875rem`, `1.5rem`
- [ ] Font weights: `400` (regular), `500` (medium), `700` (bold)
- [ ] Line heights: Tight for headings, comfortable for body

### Colors
- [ ] Background: `var(--white)` (auto-switches in dark mode)
- [ ] Text: `var(--neutral-900)`
- [ ] Borders: `var(--neutral-700)` or `var(--neutral-300)`
- [ ] Accent: `var(--orange-500)` for active states
- [ ] Delete/danger: `var(--red-500)` or `var(--red-600)`

### Dark Mode
- [ ] Test with `prefers-color-scheme: dark`
- [ ] Verify all colors invert properly
- [ ] Check border visibility
- [ ] Ensure text remains readable

---

## 🏗 Component Migration Template

### Step 1: Analyze Current Component

**Questions to ask**:
1. What is the component's purpose?
2. What are the key actions/interactions?
3. Does it need mobile optimization?
4. Are there any state changes (enabled/disabled, active/inactive)?

### Step 2: Apply Design Language

**HTML Structure**:
```html
<!-- Container with sharp borders -->
<div class="card-container">
  
  <!-- Header (if needed) -->
  <div class="card-header border-b">
    <h2 class="card-title">Component Title</h2>
  </div>
  
  <!-- Content area -->
  <div class="content-wrapper">
    <!-- Your content here -->
  </div>
  
  <!-- Actions (if needed) -->
  <div class="actions-area">
    <button class="btn-action-lg">Primary Action</button>
    <button class="btn-action-sm">Secondary</button>
  </div>
  
</div>
```

**CSS Pattern**:
```css
/* Component container - sharp edges */
.component-name {
    background-color: var(--white) !important;
    border: 1px solid var(--neutral-700) !important;
    border-radius: 0 !important;  /* Sharp corners */
    font-family: var(--font-mono) !important;
    padding: 1.5rem !important;
}

/* Dark mode support */
[data-theme="dark"] .component-name {
    border-color: var(--neutral-400) !important;
}

/* Mobile stacking */
@media (max-width: 640px) {
    .component-name {
        padding: 1rem !important;
    }
}
```

### Step 3: Button Styling

**Large Action Button**:
```css
.btn-action-lg {
    border: 1px solid var(--neutral-300) !important;
    padding: 1rem !important;
    font-size: 0.75rem !important;
    font-family: var(--font-mono) !important;
    background: none !important;
    color: var(--neutral-900) !important;
    cursor: pointer !important;
    transition: all 150ms cubic-bezier(0.4, 0, 0.2, 1) !important;
}

.btn-action-lg:hover {
    border-color: var(--neutral-900) !important;
    background-color: var(--neutral-50) !important;
}
```

**Small/Compact Button**:
```css
.btn-tool {
    padding: 0.75rem !important;
    border: none !important;
    border-right: 1px solid var(--neutral-200) !important;
    background: none !important;
    color: var(--neutral-700) !important;
    cursor: pointer !important;
}

.btn-tool:hover {
    background-color: var(--neutral-100) !important;
}
```

### Step 4: Mobile Optimization

**Desktop-first approach, then stack on mobile**:
```css
/* Desktop: Horizontal layout */
.component-row {
    display: flex !important;
    gap: 1rem !important;
}

/* Mobile: Vertical stacking */
@media (max-width: 640px) {
    .component-row {
        flex-direction: column !important;
    }
}
```

---

## 🎨 Color Usage Guide

### When to Use Each Color

| Use Case | Light Mode | Dark Mode | CSS Variable |
|----------|------------|-----------|--------------|
| **Main background** | `#ffffff` | `#0a0a0a` | `var(--white)` |
| **Card background** | `#ffffff` | `#171717` | `var(--white)` |
| **Primary text** | `#171717` | `#fafafa` | `var(--neutral-900)` |
| **Secondary text** | `#737373` | `#737373` | `var(--neutral-500)` |
| **Borders (strong)** | `#404040` | `#404040` | `var(--neutral-700)` |
| **Borders (subtle)** | `#e5e5e5` | `#262626` | `var(--neutral-200)` |
| **Enabled/Active** | `#f97316` | `#ea580c` | `var(--orange-500)` |
| **Delete/Danger** | `#dc2626` | `#dc2626` | `var(--red-600)` |

### Badge Colors

```css
/* Enabled state - Orange accent */
.badge.is-enabled {
    background-color: var(--neutral-900) !important;
    color: var(--white) !important;
}

[data-theme="dark"] .badge.is-enabled {
    background-color: #262626 !important;
    border: 1px solid var(--orange-500) !important;
    color: #fafafa !important;
}

/* Disabled state - Gray */
.badge.gray {
    background-color: var(--neutral-200) !important;
    color: var(--neutral-500) !important;
}

/* Pinned state - Black/White invert */
.badge.black {
    background-color: var(--neutral-900) !important;
    color: var(--white) !important;
}
```

---

## 📱 Mobile Stacking Pattern

### Example: App Card Migration

**Desktop (Horizontal)**:
```
┌────────────────────────────────────────────┐
│ [Thumb] Title  Meta  [Badge]  [Actions]    │
└────────────────────────────────────────────┘
```

**Mobile (Stacked)**:
```
┌──────────────────┐
│ [Thumb] Title    │
│         Meta     │
│         [Badge]  │
├──────────────────┤
│ [Actions Row]    │
└──────────────────┘
```

**CSS Implementation**:
```css
/* Desktop: Flexbox horizontal */
.compact-row {
    display: flex !important;
    align-items: stretch !important;
}

/* Mobile: CSS Grid for precise control */
@media (max-width: 640px) {
    .compact-row {
        display: grid !important;
        grid-template-columns: auto 1fr auto !important;
        grid-template-rows: auto auto !important;
    }
    
    .thumb-box {
        grid-row: 1 / 2 !important;
        grid-column: 1 / 2 !important;
    }
    
    .compact-info {
        grid-row: 1 / 2 !important;
        grid-column: 2 / 3 !important;
    }
    
    .compact-status {
        grid-row: 1 / 2 !important;
        grid-column: 3 / 4 !important;
    }
    
    .compact-actions {
        grid-row: 2 / 3 !important;
        grid-column: 1 / 4 !important;
        border-top: 1px solid var(--neutral-200) !important;
    }
}
```

---

## 🔧 Common Patterns

### Pattern 1: Status Badge

```html
<div class="badge {{ if .Enabled }}is-enabled{{ else }}gray{{ end }}">
    {{ if .Enabled }}ENABLED{{ else }}DISABLED{{ end }}
</div>
```

### Pattern 2: Icon Button Toolbar

```html
<div class="compact-actions">
    <button class="btn-tool" onclick="action1()">
        <i data-lucide="play"></i>
    </button>
    <button class="btn-tool" onclick="action2()">
        <i data-lucide="edit-3"></i>
    </button>
    <button class="btn-tool btn-trash" onclick="deleteAction()">
        <i data-lucide="trash-2"></i>
    </button>
</div>
```

### Pattern 3: Metadata Row

```html
<div class="flex items-center gap-4 text-xs text-secondary">
    <div class="flex items-center gap-2">
        <i data-lucide="refresh-cw" class="meta-icon"></i>
        <span>5 min</span>
    </div>
    <div class="flex items-center gap-2">
        <i data-lucide="clock" class="meta-icon"></i>
        <span>2h ago</span>
    </div>
</div>
```

---

## ✅ Completed Example: app_card.html

**What was migrated**:
- ✅ Removed all `border-radius` (sharp corners)
- ✅ Applied monospace font throughout
- ✅ Used high-contrast monochrome palette
- ✅ Implemented mobile stacking (actions move to bottom row)
- ✅ ALL CAPS for status labels (ENABLED, DISABLED, PINNED)
- ✅ Orange accent for enabled state
- ✅ Clean, rectangular borders
- ✅ Dark mode support via CSS variables

**File location**: `/web/templates/partials/app_card.html`

---

## 🎓 Next Components to Migrate

Suggested migration order:

1. **Device Card** (`device_card.html`) - Similar to app card
2. **Playlist Card** - List/grid view components
3. **Configuration Forms** - Input fields, buttons, form layouts
4. **Navigation/Sidebar** - Menu items, links
5. **Modals/Dialogs** - Popup windows, confirmations
6. **Tables** - Data tables, lists

---

## 📚 Reference Files

- **Design System CSS**: `/web/static/css/design-system.css`
- **Completed Migration**: `/web/templates/partials/app_card.html`
- **Integration Guide**: `/INTEGRATION_GUIDE.md`
- **This Guide**: `/MIGRATION_GUIDE.md`

---

## 🚨 Common Mistakes to Avoid

1. ❌ **Forgetting `!important`**: Design system uses `!important` to override legacy CSS
2. ❌ **Using rounded corners**: Always `border-radius: 0`
3. ❌ **Wrong font**: Must use `var(--font-mono)`, not default sans-serif
4. ❌ **Ignoring mobile**: Always test at `<640px` width
5. ❌ **Hardcoded colors**: Use CSS variables for dark mode support
6. ❌ **Mixed case labels**: Use ALL CAPS for status/badge text

---

**Design Philosophy**: Content over decoration, precision over flair  
**Inspiration**: Teenage Engineering instruments
