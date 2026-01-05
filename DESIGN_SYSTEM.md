# Design System v1.0

## Overview

This is a lightweight, framework-free design system built on systematic design tokens. The system is built using vanilla CSS, HTML, and semantic class naming following the BEM methodology.

---

## Phase 1: Design Token Reference

### Color Palette

#### Backgrounds

| Token | Light Mode | Dark Mode | Usage |
|-------|-----------|-----------|-------|
| `--bg-primary` | `#fafafa` | `#0a0a0a` | Page background |
| `--bg-secondary` | `#ffffff` | `#171717` | Card backgrounds |
| `--bg-tertiary` | `#f5f5f5` | `#262626` | Hover states, elevated elements |
| `--bg-elevated` | `#ffffff` | `#262626` | Overlays, modals |

#### Borders

| Token | Light Mode | Dark Mode | Usage |
|-------|-----------|-----------|-------|
| `--border-primary` | `#171717` | `#404040` | Main borders, card outlines |
| `--border-secondary` | `#e5e5e5` | `#525252` | Dividers, subtle separators |
| `--border-tertiary` | `#d4d4d4` | `#737373` | Nested dividers |
| `--border-subtle` | `#f5f5f5` | `#262626` | Barely visible separators |

#### Text

| Token | Light Mode | Dark Mode | Usage |
|-------|-----------|-----------|-------|
| `--text-primary` | `#171717` | `#fafafa` | Headings, primary content |
| `--text-secondary` | `#737373` | `#a3a3a3` | Supporting text, labels |
| `--text-tertiary` | `#a3a3a3` | `#737373` | Muted text, placeholders |
| `--text-disabled` | `#d4d4d4` | `#525252` | Disabled state text |
| `--text-inverse` | `#ffffff` | `#0a0a0a` | Text on dark/light backgrounds |

#### Accents

| Token | Value | Usage |
|-------|-------|-------|
| `--accent-orange-500` | `#f97316` | Primary action color |
| `--accent-orange-600` | `#ea580c` | Hover state for orange |
| `--accent-red-500` | `#ef4444` | Destructive actions, errors |
| `--accent-red-600` | `#dc2626` | Hover state for red |
| `--accent-blue-400` | `#60a5fa` | Informational elements |
| `--accent-yellow-300` | `#fcd34d` | Warnings, highlights |

#### Semantic States

| Token | Light Mode | Dark Mode | Usage |
|-------|-----------|-----------|-------|
| `--state-enabled` | `#171717` | `#000000` | Enabled badge background |
| `--state-disabled` | `#e5e5e5` | `#404040` | Disabled badge background |
| `--state-pinned` | `#171717` | `#fafafa` | Pinned state background |
| `--state-active` | `#f97316` | `#f97316` | Active/playing state |
| `--state-error` | `#ef4444` | `#ef4444` | Error states |

---

### Typography

#### Font Families

| Token | Value | Usage |
|-------|-------|-------|
| `--font-mono` | `ui-monospace, SFMono-Regular, Menlo, Monaco, ...` | Primary interface font |
| `--font-sans` | `-apple-system, BlinkMacSystemFont, ...` | Alternative sans-serif |

#### Font Sizes

| Token | Value | Pixels | Usage |
|-------|-------|--------|-------|
| `--font-size-xs` | `0.75rem` | 12px | Small labels, metadata |
| `--font-size-sm` | `0.875rem` | 14px | Body text, buttons |
| `--font-size-base` | `1rem` | 16px | Base body text |
| `--font-size-lg` | `1.125rem` | 18px | Subheadings |
| `--font-size-xl` | `1.25rem` | 20px | Card titles |
| `--font-size-2xl` | `1.5rem` | 24px | Page headings |

#### Font Weights

| Token | Value | Usage |
|-------|-------|-------|
| `--font-weight-normal` | `400` | Body text |
| `--font-weight-medium` | `500` | Buttons, labels |
| `--font-weight-semibold` | `600` | Badges, emphasis |
| `--font-weight-bold` | `700` | Headings, strong emphasis |

#### Line Heights

| Token | Value | Usage |
|-------|-------|-------|
| `--line-height-tight` | `1.25` | Headings, compact text |
| `--line-height-normal` | `1.5` | Body text |
| `--line-height-relaxed` | `1.625` | Long-form content |

---

### Layout & Geometry

#### Border Radius

| Token | Value | Pixels | Usage |
|-------|-------|--------|-------|
| `--radius-none` | `0` | 0px | Sharp corners |
| `--radius-sm` | `0.125rem` | 2px | Subtle rounding |
| `--radius-md` | `0.25rem` | 4px | Standard components |
| `--radius-lg` | `0.5rem` | 8px | Cards, panels |
| `--radius-full` | `9999px` | — | Pills, circles |

#### Border Width

| Token | Value | Usage |
|-------|-------|-------|
| `--border-width-thin` | `1px` | Default borders |
| `--border-width-medium` | `2px` | Emphasis borders |
| `--border-width-thick` | `4px` | Heavy borders |

#### Spacing Scale (4px Grid)

| Token | Value | Pixels | Usage |
|-------|-------|--------|-------|
| `--space-0` | `0` | 0px | No spacing |
| `--space-1` | `0.25rem` | 4px | Tight spacing |
| `--space-2` | `0.5rem` | 8px | Compact padding |
| `--space-3` | `0.75rem` | 12px | Standard gaps |
| `--space-4` | `1rem` | 16px | Medium padding |
| `--space-5` | `1.25rem` | 20px | Comfortable spacing |
| `--space-6` | `1.5rem` | 24px | Large gaps |
| `--space-8` | `2rem` | 32px | Section spacing |
| `--space-10` | `2.5rem` | 40px | Large sections |
| `--space-12` | `3rem` | 48px | Page-level spacing |

---

### Effects

#### Shadows

| Token | Value | Usage |
|-------|-------|-------|
| `--shadow-sm` | `0 1px 2px 0 rgba(0,0,0,0.05)` | Subtle depth |
| `--shadow-base` | `0 1px 3px 0 rgba(0,0,0,0.1)` | Default elevation |
| `--shadow-md` | `0 4px 6px -1px rgba(0,0,0,0.1)` | Cards, dropdowns |
| `--shadow-lg` | `0 10px 15px -3px rgba(0,0,0,0.1)` | Modals, popovers |

#### Transitions

| Token | Value | Usage |
|-------|-------|-------|
| `--transition-fast` | `100ms cubic-bezier(0.4,0,0.2,1)` | Quick feedback |
| `--transition-base` | `150ms cubic-bezier(0.4,0,0.2,1)` | Standard transitions |
| `--transition-slow` | `300ms cubic-bezier(0.4,0,0.2,1)` | Deliberate animations |

---

## Phase 2: Component Architecture

### App Card Component

The App Card is the first atomic component in our design system. It follows a systematic, token-based approach.

#### Component Structure

```
.app-card
├── .app-card__preview (2:1 aspect ratio container)
│   ├── .app-card__preview-image
│   └── .app-card__preview-overlay (conditional)
│       └── .app-card__preview-overlay-badge
├── .app-card__info
│   ├── .app-card__title
│   └── .app-card__meta
│       └── .app-card__meta-item (repeatable)
├── .app-card__status
│   └── .app-card__badge (with modifiers)
└── .app-card__actions
    └── .app-card__action-btn (repeatable, with modifiers)
```

#### BEM Naming Convention

- **Block**: `.app-card` — The root component
- **Element**: `.app-card__preview` — Child element (uses `__`)
- **Modifier**: `.app-card__badge--enabled` — Variant state (uses `--`)
- **State**: `.is-enabled` — JavaScript-driven state (uses `.is-`)

#### The Preview Rule

The preview image container maintains a **strict 2:1 aspect ratio**:

```css
.app-card__preview {
    width: 10rem;   /* 160px */
    height: 5rem;   /* 80px - maintains 2:1 ratio */
}
```

The image uses `object-fit: contain` to ensure the **full image is visible** without cropping:

```css
.app-card__preview-image {
    width: 100%;
    height: 100%;
    object-fit: contain;
}
```

#### Badge Variants

| Class | Usage |
|-------|-------|
| `.app-card__badge--enabled` | Enabled state with orange border |
| `.app-card__badge--disabled` | Disabled gray state |
| `.app-card__badge--pinned` | Pinned state |
| `.app-card__badge--autopin` | Auto-pin indicator |

#### Action Button States

| Class | Usage |
|-------|-------|
| `.app-card__action-btn--play.is-enabled` | Orange background when enabled |
| `.app-card__action-btn--pin.is-pinned` | Inverted colors when pinned |
| `.app-card__action-btn--delete:hover` | Red background on hover |

---

## Implementation Guide

### Step 1: Include the Design System

Add to your HTML `<head>`:

```html
<link rel="stylesheet" href="/static/css/design-system.css">
```

### Step 2: Update Template Reference

Replace the old app card template with the new one:

```go
// In your Go template file
{{ template "app_card" . }}
```

### Step 3: Maintain Existing Functionality

The redesigned card maintains all existing functionality:

- ✅ Click-to-expand interaction
- ✅ Drag-and-drop support
- ✅ Enable/disable toggle
- ✅ Pin/unpin functionality
- ✅ Edit, preview, duplicate, delete actions
- ✅ Inactive overlay for empty renders
- ✅ Localization support
- ✅ Accessibility (ARIA labels)

---

## Design Principles

### 1. Token-Driven Everything

Every visual property references a design token. Never use hard-coded values in component styles.

**Good:**
```css
.my-component {
    color: var(--text-primary);
    padding: var(--space-4);
}
```

**Bad:**
```css
.my-component {
    color: #171717;
    padding: 16px;
}
```

### 2. Automatic Dark Mode

The system uses `prefers-color-scheme` to automatically switch between light and dark modes. Components inherit the correct tokens.

### 3. Systematic Spacing

Always use the 4px spacing scale (`--space-*`). This ensures visual consistency.

### 4. Semantic Class Names

Use BEM methodology for clarity:
- `.block` — Component root
- `.block__element` — Child component
- `.block__element--modifier` — Variant
- `.is-state` — Dynamic state

### 5. No Frameworks Required

This system uses only vanilla CSS Grid and Flexbox. No CSS framework dependencies.

---

## Browser Support

- ✅ Modern browsers (Chrome, Firefox, Safari, Edge)
- ✅ CSS Custom Properties (variables)
- ✅ CSS Grid & Flexbox
- ✅ `prefers-color-scheme` media query

---

## Migration Path

To migrate existing components to the design system:

1. **Audit current styles** — Identify hard-coded colors, spacing, typography
2. **Map to tokens** — Replace values with design tokens
3. **Adopt BEM naming** — Rename classes to follow BEM convention
4. **Test dark mode** — Verify components work in both light and dark modes
5. **Remove framework dependencies** — Replace W3.CSS, Bootstrap, etc.

---

## Next Steps

### Expand the Component Library

Build additional atomic components:
- Buttons (primary, secondary, destructive)
- Form inputs (text, select, checkbox)
- Modals and dialogs
- Navigation components
- Data tables

### Create Composition Patterns

Document how to combine atomic components into larger patterns:
- App list view (multiple cards)
- Settings panels
- Dashboard layouts

### Establish Design Review Process

Before adding new components:
1. Extract tokens if new values are needed
2. Follow BEM naming convention
3. Ensure dark mode compatibility
4. Test responsive behavior
5. Document usage in this guide

---

## File Structure

```
/web
├── static/
│   └── css/
│       └── design-system.css          # Design tokens + App Card component
└── templates/
    └── partials/
        ├── app_card.html               # Original (deprecated)
        └── app_card_redesign.html      # New systematic version
```

---

## Maintenance

### Adding New Tokens

When adding new tokens:

1. Add to the `:root` section in `design-system.css`
2. Add dark mode override in `@media (prefers-color-scheme: dark)`
3. Document in this file under the appropriate section
4. Update the design token table

### Versioning

This design system follows semantic versioning:

- **Major**: Breaking changes to token names or component structure
- **Minor**: New components or non-breaking token additions
- **Patch**: Bug fixes, documentation updates

**Current Version**: `1.0.0`

---

## Credits

**Design Philosophy**: Minimalist, high-performance, framework-free
**Inspiration**: Apple/Meta design systems
**Aesthetic**: Clean, systematic, maintainable
**Built for**: Millions of users, low resource consumption
