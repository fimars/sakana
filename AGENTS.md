# AGENTS.md

## Project style

- Source code is the single source of truth. Avoid reading extra `.md` files unless absolutely necessary.

## Build, run, test

- `go build ./...` — compile
- `go run .` — run from source
- `go tool mygo build` — package `build/darwin-arm64/Sakana.app` and a `.dmg`
- `tools/e2e.sh` — the test: builds, launches the app with `SAKANA_E2E_SOCK`,
  asserts the window, viewport, spring, settings, image and rest reminder
  behavior, captures PNGs, then packages the app and checks its bundle.
  Artifact: `artifacts/e2e/report.md`.
- `tools/bench.sh` — the benchmark: packages the app, launches it three times
  with a fresh userdata directory to time startup, then drives one running app
  to time the greeting sway, forced refreshes, the settings window, a character
  swap, and what the app holds at rest and while it sways. Artifact:
  `artifacts/bench/report.md`, in integer milliseconds; raw values in
  `artifacts/bench/bench.json`.

Mygo is pinned to v0.6.2 in `go.mod`; its API is 0.x and moves fast, so read the
release notes before upgrading. There are no unit tests: the E2E script is the
test, and the benchmark only measures.

Both harnesses run under `caffeinate`: Mygo holds a window's frames while
nothing shows it, so a sleeping display freezes the pet mid-sway and the
checks see a stopped app. `tools/bench.sh` also runs the scenario app with
Always on Top on, because at the default window level the front app covers the
pet and the sway pauses.

## Startup

The pet is on screen about 130–190 ms after the process starts on this machine,
and nearly all of that is Mygo/AppKit init (~75–90 ms to `WhenReady`) and the
first pet window (~50 ms), before any of this app's code runs. Keep setup off
the way to the first frame: the pet window is created first, the settings
window is created on its first open (`app.settingsWindow`), and the menu bar
icon is created from `Window.OnReadyToShow`, which runs after the first frame
reaches the window (`app.createTray`). Anything done in `setup` before that
delays the pet's appearance one-for-one.

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
- `Window.Update` and `Window.Invalidate` post to the main thread and return
  before the frame is built. Widget state is read and written on the main
  thread only; goroutines (the E2E handlers, the file dialog) go through
  `mygo.RunOnMain`.
- A frame runs `Widget.View` at most once unless a pass consumed input, and not
  at all for a paint-only frame. The widget counts a frame when `Context.Now()`
  advances (`internal/pet/stats.go`), and `GET /stats` exposes the counters with
  the app's startup phases, the process CPU time and its heap.
- A window whose content nothing shows has its frames held, and the display
  link is paused at rest: an occluded pet draws nothing and an idle app uses no
  CPU. The greeting sway runs once per launch (and per character change) and
  takes about 10 s to come to rest at 100 fps; while it runs Mygo keeps the
  frame it drew, so the app's footprint is higher until the pet rests.
- A spring at rest is not stepped: its residual motion is below `Resting`'s
  threshold but stepping it from a redraw's frame would turn it back into a
  short swing. `Widget.View` only steps a spring that is still moving.
- Click-through is whole-window only (`Window.SetIgnoreMouseEvents`); there is
  no per-region hit testing. The pet window is sized to the character's bounding
  box, so its transparent margins are small.
- A `Frameless` window on macOS is a titled `NSWindow` with a hidden title bar,
  not a borderless one: it has square corners.
- The settings window is a normal window. Showing it activates the accessory
  app, which is what lets its controls take the keyboard. It is 800 DIPs tall,
  less when the display's work area is shorter, and its content scrolls when
  the window cannot show all of it.
- The rest reminder toast is a second non-activating panel, created when the
  first toast shows. It follows the pet's always-on-top level, stays up until
  the pointer clicks it (the X, or anywhere on it), and is not click-through:
  a click on its transparent margins dismisses it too. A settings change that
  restarts the cycle, or quitting, hides it. The E2E harness dismisses it with
  `POST /toast/dismiss`. A timer goroutine runs the work/rest cycle
  (`internal/reminder`); only the toast reaches the main thread, through
  `mygo.RunOnMain`.
- `OnBeforeQuit` runs before Mygo closes the windows, and `OnWillQuit` after,
  when a closed window's `Bounds` is the zero rectangle: the app saves the
  pet's position and stops the reminder in `OnBeforeQuit`.
- `Window.CapturePage` of a native-UI window can lose its text when another
  window paints a frame before the capture: scenes point at glyphs in the
  process-wide transient atlas, and the next frame frees them (Mygo 0.6.2). An
  on-screen window is not affected, and a capture taken right after the window
  drew (or while no other window animates) is correct. The E2E harness captures
  the settings window immediately after opening it for this reason; wait for
  the pet to come to rest before capturing it for screenshots.

## Structure

- `main.go` — lifecycle, pet window, menu bar item, settings wiring, E2E hook
- `internal/pet` — spring (`state.go`), viewport (`layout.go`), widget (`widget.go`),
  image (`image.go`), frame telemetry (`stats.go`)
- `internal/reminder` — the work/rest reminder cycle (`scheduler.go`)
- `internal/settings` — store (`store.go`) and settings window (`view.go`)
- `internal/e2e` — control server for the harnesses (`/stats` and the process's CPU time in `stats.go`)
- `tools/e2e.sh`, `tools/bench.sh` — the test and the benchmark
