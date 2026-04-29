# Tronbyt Design System (Settings Framework)

This document describes the modern design language and component library introduced in the `sleep-and-ui` branch. This framework is primarily used for the Settings and Admin interfaces to provide a clean, consistent, and responsive user experience.

## Core Principles

- **Card-Based Layout**: Group related settings into visually distinct cards.
- **Consistent Spacing**: Use a standardized grid for labels and controls.
- **Subtle Interactions**: Provide clear hover states and transitions without being loud.
- **Hierarchical Information**: Use eyebrows, headers, and help text to guide the user.

## CSS Framework

All styles are defined in `web/static/css/settings-framework.css`.

### Layout Containers

| Class | Purpose |
|-------|---------|
| `.settings-page` | The main wrapper for a settings view. Provides vertical spacing between sections. |
| `.settings-two-pane` | A responsive two-column grid (Label/Description on left, Control on right). |
| `.settings-control-stack` | Vertically stacks multiple controls or inputs. |

### Cards

Cards are the primary unit of organization.

```html
<section class="settings-card">
    <div class="settings-card-header">
        <h2>Section Title</h2>
        <p>Description text for the entire card.</p>
    </div>
    <!-- Field list or content goes here -->
</section>
```

| Class | Purpose |
|-------|---------|
| `.settings-card` | The main card container with rounded corners, border, and subtle shadow. |
| `.settings-card-header` | Wrapper for the card title (`h2`) and description (`p`). |
| `.settings-card-subsection` | Used for dividing a single card into smaller titled sections. |

### Forms and Field Lists

Used to align labels and controls in a consistent grid.

```html
<div class="settings-field-list">
    <div class="settings-field-row">
        <div class="settings-field-label">
            <label for="id">Label Text</label>
        </div>
        <div class="settings-field-control">
            <input type="text" id="id" class="settings-input">
            <p class="settings-help">Helper text goes here.</p>
        </div>
    </div>
</div>
```

| Class | Purpose |
|-------|---------|
| `.settings-field-list` | Container for multiple field rows. Adds dividers between rows automatically. |
| `.settings-field-row` | A grid row containing a label and a control. Responsive (stacks on mobile). |
| `.settings-field-label` | The left-hand side of a row. Uses bold weight and consistent padding. |
| `.settings-field-control` | The right-hand side of a row for inputs, toggles, or displays. |

### Typography

| Class | Element | Purpose |
|-------|---------|---------|
| `.settings-eyebrow` | `p` | Small, uppercase text above the main page header. |
| `.settings-page-copy` | `p` | Lead text at the top of a page. |
| `.settings-help` | `p` | Small, muted text placed under a control to explain it. |
| `.settings-caption` | `span/p`| Small, muted text used for metadata or subtle links (e.g., "Expand"). |
| `.settings-tag` | `span` | Pills for statuses like "Admin" or counts. |

### Inputs and Controls

Standardized sizing for inputs.

- `.settings-input`: Default width (max 28rem).
- `.settings-input-wide`: Extended width (max 40rem).
- `.settings-input-xwide`: Extra wide (max 52rem).
- `.settings-input-code`: Monospace font for keys or technical values.

### Navigation

The sub-navigation bar used at the top of the settings area.

```html
<nav class="settings-subnav">
    <a href="..." class="settings-subnav-link active">Account</a>
    <a href="..." class="settings-subnav-link">Admin</a>
</nav>
```

### Dialogs

Modern modal dialogs using the native `<dialog>` element.

| Class | Purpose |
|-------|---------|
| `.settings-dialog` | Modern modal style with backdrop support and deep shadows. |
| `.settings-dialog-actions`| Flex container for buttons at the bottom of a dialog. |

### Interactive Elements

- **Subtle Toggles**: Use `.settings-caption` with inline styles (`cursor: pointer; user-select: none; text-decoration: none;`) for links that reveal advanced sections (e.g., "Expand").
- **Actions Area**: Use `.settings-actions` for groups of buttons (Save, Cancel, Delete).
