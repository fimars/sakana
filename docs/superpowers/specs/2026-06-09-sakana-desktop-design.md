# Sakana Desktop Widget — Design Spec

## Overview

A native macOS desktop widget that replicates the [sakana-widget](https://github.com/dsrkafuu/sakana-widget) physics animation. Displays a user-selected image as a chibi character on a bottom-mounted stand, with spring-physics swaying motion. The widget has zero on-component interaction — all configuration via a menu bar icon and independent settings panel.

### Settings

1. **Local image** — file picker for a PNG/JPEG to use as the character
2. **Always on top** — toggle window level between floating and normal
3. **Screen & position** — select target display, set coordinates (or drag a positioning panel)

---

## Physics Model

Based on the original sakana-widget source (`src/index.ts:_run()`). A damped harmonic oscillator using semi-implicit Euler integration.

### State Variables

| Variable | Meaning | Default |
|----------|---------|---------|
| `i` (inertia) | Response speed per frame | 0.08 |
| `s` (stickiness) | Drag sensitivity | 0.1 |
| `d` (decay) | Velocity retention per frame | 0.99 |
| `r` (rotation) | Current angle in degrees | 0 |
| `y` (offset) | Vertical displacement in px | 0 |
| `t` (velocityY) | Vertical velocity | 0 |
| `w` (velocityR) | Angular velocity | 0 |

### Per-Frame Update (60fps target)

```
// Angular motion
w = w - r * 2              // spring acceleration = -2θ
r = r + w * i * 1.2        // Euler step
w = w * d                  // velocity damp

// Vertical motion
t = t - y * 2              // spring acceleration = -2y
y = y + t * i * 2          // Euler step
t = t * d                  // velocity damp
```

- If frame interval ≤ 16ms: scale inertia fractionally: `_inertia = i * frameDiff / 16.67`
- Stop animation when `max(|w|, |r|, |t|, |y|) < threshold` (threshold = 0.1)
- Initial kick: start with non-zero `r` and `y` so the widget swings on first render

### Drag Behavior

During drag:
- `r = deltaX * s` (clamped to ±maxR)
- `y = deltaY * s * 2` (clamped to ±maxY)
- `w = 0; t = 0` (velocities zeroed)

On release: spring force recovers naturally from the clamped position.

### Limits

- `maxR = clamp(size / 5, 30, 60)` degrees
- `maxY = size / 4` px
- `minY = -maxY` px

---

## Module Architecture

```
SakanaDesktop/
├── App.swift                    // @main entry, AppGroup scenes
├── Physics/
│   ├── SakanaState.swift        // Physics state struct
│   └── SakanaAnimator.swift     // CADisplayLink → @ObservableObject
├── Window/
│   └── SakanaWindow.swift       // NSWindowRepresentable / WindowGroup
├── View/
│   ├── SakanaWidgetView.swift   // Root widget view (base + character + rod)
│   ├── CharacterImageView.swift // NSImage view with rotation + offset
│   └── RodShape.swift           // Canvas/Shape for the spring rod line
├── Settings/
│   ├── SettingsView.swift       // Settings panel UI
│   └── SettingsStore.swift      // AppStorage + UserDefaults persistence
└── Menubar/
    └── MenuBarView.swift        // MenuBarExtra scene
```

Skip the original's canvas-based rendering — SwiftUI handles layout natively.

---

## Window Behavior

- **Type**: Borderless, transparent (`NSWindow.StyleMask.borderless`, `isOpaque = false`, `backgroundColor = .clear`)
- **Level config**: Normal or floating via `NSWindow.Level`, controlled by settings toggle
- **Position**: Fixed, set via settings panel (screen selector + coordinate input)
- **Default position**: Screen bottom-right, 64px from edges
- **Collection behavior**: `canJoinAllSpaces` so widget follows across desktops
- **Not movable by user drag**: Window position is locked; repositioning only through settings
- **ignoresMouseEvents**: false for character area (must receive drag for physics), true elsewhere

### Window Size

- Fixed default: 200×200pt widget, expandable to 120×120pt minimum
- Image size: `widgetSize / 1.25` within the window

## Drag Interaction

The character image supports spring-physics dragging, identical to the original:
- **Drag character** → character bends/stretches following the cursor (physics drag, r and y updated from delta)
- **Release** → character springs back naturally via the damped harmonic oscillator
- **Window does NOT move** — drag only affects physics state, not window position
- Base/stand area does not react to drag events

---

## Rendering Pipeline

Per CADisplayLink frame:

1. `SakanaAnimator` fires
2. Call `SakanaState.update(deltaTime:)`
3. State publishes changes (via `@Published`)
4. SwiftUI body redraws:
   - `RodShape` — a Path line from base center (0,0 of rod space) to the character's attach point, calculated from `r` and `y`
   - `CharacterImageView` — `.rotationEffect(.degrees(r))` + `.offset(y: y)` (horizontal offset `= r * 1` like original)

### Layout

```
┌──────────────┐
│              │
│   🐟 (角色)  │  ← rotationEffect + offset
│     ╱        │  ← rod line (dynamic endpoints)
│   ╱          │
│ ▔▔▔▔▔▔▔▔▔▔  │  ← static base bar
└──────────────┘
```

---

## Settings

### Persistent Storage (AppStorage)

| Key | Type | Default |
|-----|------|---------|
| `characterImagePath` | String (bookmark) | nil (uses default fish placeholder) |
| `alwaysOnTop` | Bool | false |
| `screenName` | String | "" (primary display) |
| `positionX` | Double | auto-calc bottom-right 64px |
| `positionY` | Double | auto-calc bottom-right 64px |

### Settings Window

- Title: "Sakana Settings"
- **Image Picker**: NSOpenPanel, saves security-scoped bookmark
- **Always-on-top**: Toggle switch
- **Screen & Position**:
  - Dropdown: list all connected displays (e.g. "Built-in Retina Display", "DELL U2723QE")
  - X/Y coordinate fields (editable)
  - Or: a floating positioning panel that can be dragged to preview the position
- **Apply / Close**: Move widget to selected screen at specified coordinates

### Menu Bar

- MenuBarExtra with "Settings…", "About…", "Quit" items

---

## Testing

- Physics correctness: unit test comparing update cycle against reference values from original JS
- Snapshot test for layout at known state values
- Manual: drag behavior, window positioning, settings persistence

---

## Dependencies

- None (pure SwiftUI + AppKit bridging as needed)
- Minimum deployment: macOS 14.0 (Sonoma)
