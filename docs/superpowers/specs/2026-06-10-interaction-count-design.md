# Interaction Count

**Date**: 2026-06-10
**Status**: Draft

## Overview

Add a persistent cumulative counter that tracks how many times the user physically interacts with the character widget. Each complete gesture cycle (drag start→end or hover enter→exit) counts as one interaction.

## Scope

- Persist interaction count across app sessions via `@AppStorage`
- Increment on each gesture cycle (drag released, hover ended)
- Display total count in Settings panel
- Provide a reset button to zero the counter

### Not in scope

- Per-day breakdown or timestamped event log
- Export / analytics
- Counting non-physical interactions (settings changes, menu bar clicks)

## Design

### Storage

```
@AppStorage("interactionCount") var interactionCount: Int = 0
```

Added to `SettingsStore`, alongside existing `@AppStorage` properties. Int is large enough — no overflow concern for a gesture counter.

### Increment Trigger

The count increments at the gesture-cycle boundary, not every frame:

| Mode | Trigger Point | Code Location |
|------|--------------|---------------|
| Drag | `onEnded` closure of `DragGesture` | `SakanaWidgetView.body` |
| Hover | Completion handler of hover end (future `onContinuousHover` ended phase) | `SakanaWidgetView.body` (when hover mode is implemented) |

Since hover mode is not yet implemented, the initial implementation only hooks into the drag `onEnded`. The hover hook is noted as a forward-compatible instruction in the spec.

### Settings UI

A new `Section("Statistics")` in `SettingsView`, placed below the Position section and above the Apply button:

```
Section("Statistics") {
    HStack {
        Text("Interactions")
        Spacer()
        Text("\(settings.interactionCount)")
            .foregroundColor(.secondary)
            .monospacedDigit()
    }
    Button("Reset Counter") {
        settings.interactionCount = 0
    }
}
```

- `monospacedDigit()` keeps the number width stable as digits change
- Reset button is destructive in spirit but simple — no confirmation dialog needed for a counter

### No API Changes

No new types, protocols, or notifications. The counter is purely `@AppStorage` reads/writes. The view layer directly increments the property — no abstraction layer needed for a single counter.

## Files Changed

| File | Change |
|------|--------|
| `Sources/SakanaDesktop/Settings/SettingsStore.swift` | Add `@AppStorage("interactionCount")` property |
| `Sources/SakanaDesktop/Settings/SettingsView.swift` | Add Statistics section with counter display and reset button |
| `Sources/SakanaDesktop/View/SakanaWidgetView.swift` | Add `settings.interactionCount += 1` in drag `.onEnded` |

## Verification

- `swift build` compiles cleanly
- Manual: drag character and release → count increments by 1 in Settings
- Manual: drag multiple times → count increases linearly
- Manual: click "Reset Counter" → count resets to 0, persists after reset
- Manual: quit and relaunch app → count survives across sessions
- Future: hover enter/exit also increments by 1 (when hover mode is implemented)
