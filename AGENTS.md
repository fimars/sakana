# AGENTS.md

## Project style

- Source code is the single source of truth. Avoid reading extra `.md` files unless absolutely necessary.

## Known issues

### Settings window cannot gain key focus under `.accessory` policy

`MenuBarExtra` apps default to the `.accessory` activation policy (no Dock icon). Under this policy, no window can become the key window, so text fields and buttons in the settings panel become unresponsive.

Current workaround (`AppDelegate.showSettings()`):

```swift
NSApp.setActivationPolicy(.regular)
```

macOS offers no middle ground — there is no way to let a single window receive key focus while staying `.accessory`. The policy is toggled once and not reverted (stays `.regular` until quit), which has no practical impact for this use case.
