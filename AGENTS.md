# AGENTS.md

## Project style

- Source code is the single source of truth. Avoid reading extra `.md` files unless absolutely necessary.

## Build, run, test

- `go build ./...` — compile
- `go run .` — run from source
- `go tool mygo build` — package `build/darwin-arm64/Sakana.app` and a `.dmg`
- `tools/e2e.sh` — the test: builds, launches the app with `SAKANA_E2E_SOCK`,
  asserts the window, viewport, spring, settings and image behavior, captures
  PNGs, then packages the app and checks its bundle. Artifact:
  `artifacts/e2e/report.md`.

Mygo is pinned to v0.6.2 in `go.mod`; its API is 0.x and moves fast, so read the
release notes before upgrading. There are no unit tests: the E2E script is the
test.

## Mygo notes for this app

- The pet window is a `Panel` (an `NSPanel` with the non-activating style): it
  floats over every app and Space, stays out of the Dock and the app switcher,
  and clicks do not activate Sakana. `LSUIElement` in `mygo.json` alone is not
  enough, because `App.Run` sets the activation policy explicitly: `main.go`
  calls `App.SetActivationPolicy(Accessory)`.
- `Panel` forces always-on-top; the "Always on Top" setting lowers the window
  level with `Window.SetAlwaysOnTop(false)`.
- Native UI shows the desktop through the window only when the root is
  transparent: `c.Root().Background(ui.Transparent)`.
- `Window.Update` and `Window.Invalidate` post to the main thread. Widget state
  is read and written on the main thread only; goroutines (the E2E handlers, the
  file dialog) go through `mygo.RunOnMain`.
- Click-through is whole-window only (`Window.SetIgnoreMouseEvents`); there is
  no per-region hit testing. The pet window is sized to the character's bounding
  box, so its transparent margins are small.
- A `Frameless` window on macOS is a titled `NSWindow` with a hidden title bar,
  not a borderless one: it has square corners.
- The settings window is a normal window. Showing it activates the accessory
  app, which is what lets its controls take the keyboard.
- `Window.CapturePage` of a native-UI window can lose its text when another
  window paints a frame before the capture: scenes point at glyphs in the
  process-wide transient atlas, and the next frame frees them (Mygo 0.6.2). An
  on-screen window is not affected, and a capture taken right after the window
  drew (or while no other window animates) is correct. The E2E harness captures
  the settings window immediately after opening it for this reason; wait for
  the pet to come to rest before capturing it for screenshots.

## Structure

- `main.go` — lifecycle, pet window, menu bar item, settings wiring, E2E hook
- `internal/pet` — spring (`state.go`), viewport (`layout.go`), widget (`widget.go`), image (`image.go`)
- `internal/settings` — store (`store.go`) and settings window (`view.go`)
- `internal/e2e` — control server for `tools/e2e.sh`
