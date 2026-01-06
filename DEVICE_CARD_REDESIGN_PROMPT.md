# Device Card Redesign Prompt

Redesign and migrate `device_card.html` to the Teenage Engineering-inspired design system, using `app_card.html` as the reference implementation.

**Context:**
This is NOT just a CSS class swap—we're redesigning the device card to match our sharp, instrument-like aesthetic. Think Teenage Engineering OP-1: precise controls, monospace readouts, tactile button grids.

**Reference Files:**
- `app_card.html` - Gold standard (compact/full view pattern)
- `design-system.css` - All available CSS classes
- `MIGRATION_GUIDE.md` - Component migration template

---

## REDESIGN REQUIREMENTS

### 1. Brightness Control → Instrument-Style Button Grid

**Current**: 6 buttons (0-5) in a row  
**New Design**: Sharp rectangular button grid, Teenage Engineering style

```
┌────┬────┬────┬────┬────┬────┐
│ 0  │ 1  │ 2  │ 3  │ 4  │ 5  │
└────┴────┴────┴────┴────┴────┘
```

**Requirements:**
- Sharp rectangular buttons (border-radius: 0)
- Monospace numbers
- Active state: Orange fill (var(--orange-500))
- Inactive: Outlined with neutral border
- Equal width buttons (min-width: 3rem)
- Hover: Subtle background change
- Mobile: Maintain horizontal grid (don't stack)
- Label: "BRIGHTNESS" in ALL CAPS, monospace

**CSS Pattern:**
```css
.brightness-btn {
    border: 1px solid var(--neutral-300);
    padding: 0.75rem;
    font-family: var(--font-mono);
    background: none;
    min-width: 3rem;
}

.brightness-btn.active {
    background-color: var(--orange-500);
    border-color: var(--orange-500);
    color: #ffffff;
}
```

---

### 2. App Cycle Time → Precision Slider with Monospace Readout

**Current**: HTML range slider with value display  
**New Design**: Styled slider with large monospace readout

```
APP CYCLE TIME
┌────────────────────────────────┐
│ ████████░░░░░░░░░░░░░░░░░░░░░░ │
└────────────────────────────────┘
        15s
```

**Requirements:**
- Label: "APP CYCLE TIME" (ALL CAPS)
- Large monospace value display: "15s" (1.5rem, bold)
- Styled range input (sharp track, rectangular thumb)
- Track: Filled portion in orange, unfilled in gray
- Thumb: Sharp rectangular handle (not circular)
- Min/max labels: "1s" and "30s" at ends
- Real-time value update as user drags

**CSS Pattern:**
```css
input[type="range"] {
    -webkit-appearance: none;
    background: var(--neutral-200);
    height: 4px;
}

input[type="range"]::-webkit-slider-thumb {
    -webkit-appearance: none;
    width: 16px;
    height: 24px;
    background: var(--neutral-900);
    border-radius: 0; /* Sharp rectangle */
}
```

---

### 3. View Toggle → Compact/Full Expand ALL Pattern

**Current**: List/Grid/Collapsed buttons  
**New Design**: Single toggle to expand/collapse ALL app cards at once

```
┌──────────────┬──────────────┐
│ COMPACT ALL  │  EXPAND ALL  │
└──────────────┴──────────────┘
```

**Requirements:**
- Two-button toggle (like a switch)
- "COMPACT ALL" - Collapse all app cards to compact view
- "EXPAND ALL" - Expand all app cards to full view
- Active button: Orange background
- Inactive button: Outlined
- Sharp rectangular buttons (no rounded corners)
- Use existing toggleAppCardView() pattern from app_card.html
- JavaScript: Loop through all app cards and toggle them

**Functionality:**
```javascript
// Compact All: Collapse all app cards
function compactAllCards(deviceId) {
    document.querySelectorAll('.app-card[data-expanded="true"]')
        .forEach(card => collapseAppCard(card.dataset.iname));
}

// Expand All: Expand all app cards
function expandAllCards(deviceId) {
    document.querySelectorAll('.app-card[data-expanded="false"]')
        .forEach(card => expandAppCard(card.dataset.iname));
}
```

**Remove**: Grid view toggle (not needed with new card design)

---

## OVERALL LAYOUT REDESIGN

### Desktop Layout (Horizontal)
```
┌─────────────────────────────────────────────────────────────┐
│ DEVICE NAME                    [PINNED] [NIGHT MODE]        │
├──────────────────────┬──────────────────────────────────────┤
│                      │  ┌────────────────────────────────┐  │
│ BRIGHTNESS           │  │                                │  │
│ [0][1][2][3][4][5]   │  │     Currently Displaying       │  │
│                      │  │          [Preview]             │  │
│ APP CYCLE TIME       │  │                                │  │
│ ████████░░░░░ 15s    │  └────────────────────────────────┘  │
│                      │                                      │
│ [Add App] [Edit]     │  ▼ Device Info                       │
│ [Firmware]           │  (Collapsible)                       │
│                      │                                      │
│ [COMPACT ALL]        │                                      │
│ [EXPAND ALL]         │                                      │
└──────────────────────┴──────────────────────────────────────┘
```

### Mobile Layout (Stacked)
```
┌────────────────────────────┐
│ DEVICE NAME                │
│ [PINNED] [NIGHT MODE]      │
├────────────────────────────┤
│  ┌──────────────────────┐  │
│  │   Currently          │  │
│  │   Displaying         │  │
│  │   [Preview]          │  │
│  └──────────────────────┘  │
├────────────────────────────┤
│ BRIGHTNESS                 │
│ [0][1][2][3][4][5]         │
│                            │
│ APP CYCLE TIME             │
│ ████████░░░░░ 15s          │
│                            │
│ [Add App]                  │
│ [Edit Device]              │
│ [Firmware]                 │
│                            │
│ [COMPACT ALL]              │
│ [EXPAND ALL]               │
├────────────────────────────┤
│ ▼ Device Info              │
└────────────────────────────┘
```

---

## DESIGN SYSTEM REQUIREMENTS

✓ **Sharp & Clean**
- border-radius: 0 everywhere
- 1px solid borders
- Rectangular buttons and controls

✓ **Focused Palette**
- Monospace font (var(--font-mono))
- ALL CAPS labels
- Orange accent (var(--orange-500)) for active states
- High contrast black/white/grays

✓ **Instrument Feel**
- Precise controls (button grids, styled sliders)
- Clear readouts (large monospace values)
- Tactile feedback (hover states)

✓ **Mobile Friendly**
- Vertical stacking @media max-width: 640px
- Full-width buttons on mobile
- Touch-friendly targets (min 0.75rem padding)

---

## MAINTAIN ALL FUNCTIONALITY

- Brightness: `setBrightness('deviceId', value)`
- Cycle time: `updateInterval('deviceId', value)`
- Device info: Collapsible toggle
- All href links (Add App, Edit, Firmware)
- i18n translations
- Conditional rendering (ReadOnly, SupportsFirmware)
- App cards rendering (`{{ template "app_card" ... }}`)

---

## OUTPUT

Complete redesigned device_card.html with:
1. ✅ Teenage Engineering aesthetic
2. ✅ Redesigned brightness control (button grid)
3. ✅ Redesigned cycle time slider (styled, monospace readout)
4. ✅ New view toggle (Compact All / Expand All)
5. ✅ Sharp rectangular design throughout
6. ✅ Monospace typography
7. ✅ Mobile-responsive stacking
8. ✅ Dark mode support
9. ✅ All functionality preserved

**Think**: OP-1 synthesizer control panel, not generic web form.
