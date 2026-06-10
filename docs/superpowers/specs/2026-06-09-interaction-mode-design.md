# Interaction Mode: Hover / Drag

**Date**: 2026-06-09
**Status**: Draft

## Overview

Add a setting to switch between two mutually exclusive widget interaction modes: **hover** (character follows cursor via spring physics) and **drag** (character is pulled by drag gesture, then springs back).

## Scope

- Settings UI to select between hover and drag mode
- Hover mode: character lazily drifts toward cursor position, springs back to center when cursor leaves
- Drag mode: preserve existing drag-to-pull behavior unchanged

### Not in scope

- Simultaneous hover + drag (modes are exclusive)
- Tunable hover sensitivity / damping parameters
- Mode-specific visual indicators on the widget itself

## Design

### InteractionMode enum

```
enum InteractionMode: String, CaseIterable, RawRepresentable {
    case drag   // existing drag gesture behavior
    case hover  // cursor-follow with spring physics
}
```

Stored via `@AppStorage("interactionMode")` in `SettingsStore`, default `drag`.

### Spring Target Extension

`SakanaState` gains two new mutable fields, defaulting to 0:

```
var targetR: Double = 0
var targetY: Double = 0
```

`update(deltaTime:)` changes the spring restoring force from zero-centered to target-centered:

```
// Before:
w -= r * 2
t -= y * 2

// After:
w -= (r - targetR) * 2
t -= (y - targetY) * 2
```

When `targetR`/`targetY` are 0, behavior is identical to current (backward compatible).

### Animator API

`SakanaAnimator` gains two methods:

- `setHoverTarget(r: Double, y: Double)` — clamps values to `maxR`/`maxY`, sets target fields on state
- `clearHoverTarget()` — resets both targets to 0

The `tick(_:)` method requires no change: in drag mode `isDragging` pauses physics during drag; in hover mode physics keep running every frame.

### View Integration

`SakanaWidgetView` reads `settings.interactionMode`:

- **drag mode**: apply existing `DragGesture` (unchanged)
- **hover mode**: apply `.onContinuousHover` modifier

Hover coordinate mapping (widget size = 200×200):

```
active(location):
  dx = location.x - 100
  dy = location.y - 100
  targetR = (dx / 100) * state.maxR       // horizontal → rotation
  targetY = -(dy / 100) * state.maxY * 0.3 // vertical → lift, inverted & damped

ended:
  targetR = 0, targetY = 0
```

### Settings UI

A new `Section("Interaction")` in `SettingsView` with a `Picker` bound to `settings.interactionMode`. Labels: "Drag" and "Hover".

## Files Changed

| File | Change Summary |
|------|---------------|
| `Sources/SakanaDesktop/Settings/SettingsStore.swift` | Add `InteractionMode` enum, `@AppStorage` property |
| `Sources/SakanaDesktop/Settings/SettingsView.swift` | Add Interaction section with Picker |
| `Sources/SakanaDesktop/Physics/SakanaState.swift` | Add `targetR`, `targetY`; modify `update()` restoring force |
| `Sources/SakanaDesktop/Physics/SakanaAnimator.swift` | Add `setHoverTarget(r:y:)`, `clearHoverTarget()` |
| `Sources/SakanaDesktop/View/SakanaWidgetView.swift` | Conditional gesture / hover modifier based on mode |

## Verification

- `swift build` compiles cleanly
- Manual: run app, open Settings, switch to hover mode — character leans toward cursor and returns on leave
- Manual: switch to drag mode — drag behavior unchanged from before
- Manual: switch screens / Mission Control — hover tracking works on non-primary screens (`.canJoinAllSpaces` already set on window)
