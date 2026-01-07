# Tronbyt Design System

## Overview

This design system is inspired by **Teenage Engineering** - clean, instrument-like interfaces with sharp edges, monospace typography, and a minimal color palette.

---

## Color Palette

All colors MUST use CSS variables. No hardcoded hex values.

### Core Colors

| Variable | Light Mode | Dark Mode | Usage |
|----------|------------|-----------|-------|
| `--white` | `#ffffff` | `#171717` | Card backgrounds |
| `--black` | `#000000` | `#000000` | LED displays, pure black |
| `--bg-primary` | `#ffffff` | `#0a0a0a` | Page background |
| `--bg-secondary` | `#fafafa` | `#171717` | Card backgrounds |
| `--bg-tertiary` | `#f5f5f5` | `#262626` | Elevated/hover states |

### Neutral Palette

| Variable | Light Mode | Dark Mode | Usage |
|----------|------------|-----------|-------|
| `--neutral-50` | `#fafafa` | `#0a0a0a` | Subtle backgrounds |
| `--neutral-100` | `#f5f5f5` | `#171717` | Light backgrounds |
| `--neutral-200` | `#e5e5e5` | `#262626` | Borders, dividers |
| `--neutral-300` | `#d4d4d4` | `#404040` | Main borders |
| `--neutral-400` | `#a3a3a3` | `#525252` | Subtle dividers, disabled |
| `--neutral-500` | `#737373` | `#737373` | Muted text, icons |
| `--neutral-600` | `#525252` | `#a3a3a3` | Secondary text |
| `--neutral-700` | `#404040` | `#d4d4d4` | Body text |
| `--neutral-900` | `#171717` | `#fafafa` | Primary text, headings |

### Accent Colors

| Variable | Value | Usage |
|----------|-------|-------|
| `--orange-300` | `#fdba74` | Light accent backgrounds |
| `--orange-400` | `#fb923c` | Hover states |
| `--orange-500` | `#f97316` | Primary accent (active, selected) |
| `--orange-600` | `#ea580c` | Dark accent hover |

### Danger Colors (Delete only)

| Variable | Value | Usage |
|----------|-------|-------|
| `--red-50` | `#fef2f2` / `#450a0a` | Danger hover background |
| `--red-300` | `#fca5a5` | Danger borders |
| `--red-500` | `#ef4444` | Danger primary |
| `--red-600` | `#dc2626` | Danger hover |

---

## Typography

| Property | Value |
|----------|-------|
| `--font-mono` | `ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace` |

### Text Styles

| Class/Usage | Font Size | Font Weight | Text Transform |
|-------------|-----------|-------------|----------------|
| `.control-label` | `0.6875rem` | 700 | UPPERCASE |
| `.badge` | `0.625rem` | 600 | UPPERCASE |
| Button labels | `0.6875rem` | 600 | UPPERCASE |
| Body text | `0.875rem` | 400 | none |

---

## Border System

| Variable | Light Mode | Dark Mode | Usage |
|----------|------------|-----------|-------|
| `--border-primary` | `var(--neutral-200)` | `#404040` | Card borders |
| `--border-secondary` | `var(--neutral-300)` | `#525252` | Subtle dividers |

**Border radius**: `0` (ALWAYS - TE design uses sharp corners)

---

## Spacing Scale

| Token | Value | Usage |
|-------|-------|-------|
| `--space-xs` | `0.25rem` | Tight gaps |
| `--space-sm` | `0.5rem` | Small gaps, button gaps |
| `--space-md` | `0.75rem` | Medium padding |
| `--space-lg` | `1rem` | Large padding |
| `--space-xl` | `1.5rem` | Section spacing |

---

## Button Types

### Save/Primary Button

| State | Light Mode | Dark Mode |
|-------|------------|-----------|
| **Default** | `bg: #171717` `text: #ffffff` | `bg: #fafafa` `text: #171717` |
| **Hover** | `bg: #404040` | `bg: #e5e5e5` |

CSS Class: `.btn-pin.is-pinned`
CSS Variables: `--btn-primary-bg`, `--btn-primary-text`, `--btn-primary-hover-bg`

### Secondary Button

| State | Light Mode | Dark Mode |
|-------|------------|-----------|
| **Default** | `bg: #e5e5e5` `text: #404040` | `bg: #262626` `text: #d4d4d4` |
| **Hover** | `bg: #d4d4d4` | `bg: #404040` |

CSS Class: `.btn-action-sm`, `.btn-tool`
CSS Variables: `--btn-secondary-bg`, `--btn-secondary-text`, `--btn-secondary-hover-bg`

### Active/Selected State (Orange accent)

| State | Light Mode | Dark Mode |
|-------|------------|-----------|
| **Active** | `bg: #f97316` `text: #ffffff` | `bg: #f97316` `text: #ffffff` |
| **Hover** | `bg: #ea580c` | `bg: #ea580c` |
| **Deep Hover** | `bg: #c2410c` | `bg: #c2410c` |

CSS Class: `.is-enabled`, `.active`, `.brightness-btn.active`
CSS Variables: `--state-active-bg`, `--state-active-text`, `--state-active-hover-bg`, `--state-active-deep-hover-bg`

### Danger/Delete Button

| State | Light Mode | Dark Mode |
|-------|------------|-----------|
| **Default** | `border: #fca5a5` `text: #dc2626` `bg: transparent` | `border: #dc2626` `text: #f87171` |
| **Hover** | `bg: #fef2f2` `border: #dc2626` | `bg: #dc2626` `text: #ffffff` |

CSS Class: `.btn-delete`
CSS Variables: `--btn-danger-bg`, `--btn-danger-text`, `--btn-danger-border`, `--btn-danger-hover-bg`

---

## Component Reference

### Card Container
```css
.card-container {
    background: var(--white);
    border: 1px solid var(--neutral-200);
    border-radius: 0;
}
```

### Badge
```css
.badge {
    font-family: var(--font-mono);
    font-size: 0.625rem;
    font-weight: 600;
    text-transform: uppercase;
    padding: 0.25rem 0.5rem;
    border-radius: 0;
}

.badge.black {
    background: var(--neutral-900);
    color: var(--white);
}

.badge.gray {
    background: var(--neutral-200);
    color: var(--neutral-700);
}

.badge.is-enabled {
    background: var(--green-500);
    color: var(--white);
}
```

### Small Action Button
```css
.btn-action-sm {
    padding: 0.5rem 0.75rem;
    border: 1px solid var(--neutral-200);
    border-radius: 0;
    font-family: var(--font-mono);
    font-size: 0.6875rem;
    font-weight: 600;
    text-transform: uppercase;
    background: var(--white);
    color: var(--neutral-700);
}
```

### Tool Button (Icon only)
```css
.btn-tool {
    padding: 0.75rem;
    border: 1px solid var(--neutral-200);
    border-radius: 0;
    background: var(--white);
    color: var(--neutral-500);
}
```

### Dropdown Menu
```css
.dropdown-menu {
    position: absolute;
    border: 1px solid var(--neutral-300);
    border-radius: 0;
    background: var(--white);
    box-shadow: 0 4px 6px rgba(0,0,0,0.1);
}

.dropdown-item {
    font-family: var(--font-mono);
    font-size: 0.6875rem;
    color: var(--neutral-700);
}
```

### Device Panel Layout

The device control panel uses consistent grid patterns for visual alignment.

#### Brightness Grid
```css
.brightness-grid {
    display: grid;
    grid-template-columns: repeat(6, 1fr);
    gap: 0.5rem;
}

.brightness-btn {
    padding: 0.875rem 0.75rem;  /* Taller for visual weight */
    min-height: 3rem;
}
```

#### Action Button Grid
```css
.device-action-grid {
    display: grid;
    grid-template-columns: repeat(3, 1fr);  /* Equal width columns */
    gap: 0.5rem;
}
```

#### View Toggle Grid
```css
.view-toggle-grid {
    display: grid;
    grid-template-columns: repeat(2, auto);  /* Auto-fit to content */
    gap: 0.5rem;
    justify-content: start;
}
```

#### Section Spacing
All control groups use consistent vertical spacing:
```css
.device-controls {
    gap: 1.5rem;  /* Between sections */
}

.control-group {
    gap: 0.5rem;  /* Between label and content */
}
```

---

## Icons

**Icon Library**: Lucide Icons (via `data-lucide` attribute)

**Standard Sizes**:
- Small: `14px × 14px` (badges, inline)
- Medium: `16px × 16px` (buttons)
- Large: `18px × 18px` (action buttons)

---

## Do's and Don'ts

### ✅ DO
- Use CSS variables for ALL colors
- Use `border-radius: 0` (sharp corners)
- Use monospace font for controls
- Use UPPERCASE for labels and badges
- Use Lucide icons consistently

### ❌ DON'T
- Use hardcoded hex colors in component CSS
- Use rounded corners
- Mix icon libraries (no Font Awesome)
- Use green except for success states
- Use blue except for links

---

## Migration Checklist

When updating existing components:

1. [ ] Replace hardcoded hex colors with CSS variables
2. [ ] Remove `border-radius` (or set to 0)
3. [ ] Replace Font Awesome icons with Lucide
4. [ ] Update button classes to TE system
5. [ ] Ensure dark mode support via `[data-theme="dark"]`
