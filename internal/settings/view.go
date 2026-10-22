package settings

import (
	"fmt"
	"slices"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// Display is a screen the position picker offers.
type Display struct {
	Label    string
	WorkArea mygo.Rectangle
	Primary  bool
}

// UI is the settings window's content: the character image, whether the
// window floats, and where it sits, in the manner of the Swift
// SettingsView.
//
// The app owns the settings and what applying them does; the fields below
// are its callbacks. View builds the interface.
type UI struct {
	Store *Store

	// Bitmap is the character image to preview.
	Bitmap func() *ui.Bitmap
	// PickImage asks for an image file and applies it; it returns at once
	// and the dialog may outlive the frame.
	PickImage func()
	// RemoveImage goes back to the bundled character.
	RemoveImage func()
	// Displays lists the screens the position picker offers, the primary
	// first.
	Displays func() []Display
	// PetSize is the pet window's size in DIPs, which the offsets are
	// measured against.
	PetSize func() (w, h int)
	// Apply saves the chosen screen and offsets and moves the window.
	Apply func(screenLabel string, offsetX, offsetY float64)
	// ApplyAlwaysOnTop applies the floating toggle.
	ApplyAlwaysOnTop func(on bool)
	// Redraw requests a frame of the settings and pet windows.
	Redraw func()

	// padX and padY are the offsets the coordinate pad shows, and padChoice
	// the screen it positions on; padMaxX and padMaxY its bounds. Reset
	// fills them from Store when the window opens. padBoundsChoice is the
	// choice the bounds were measured on, which lags padChoice by a build
	// pass: a select applies its choice after the view that built it.
	padX, padY       float64
	padChoice        string
	padMaxX, padMaxY float64
	padBoundsChoice  string
	// padPressed is whether the pad was pressed in the frame before, so
	// that the first frame of a drag jumps the handle to the pointer and
	// the ones after it move it by the pointer's delta.
	padPressed bool

	lastAlwaysOnTop bool
}

// primaryOption is the picker's item for the primary display.
const primaryOption = "Primary Display"

// The coordinate pad: its size and handle in DIPs, and the spacing of its
// grid, as the Swift CoordinatePadView drew them.
const (
	padSize    = 256.0
	handleSize = 10.0
	gridStep   = 32.0
	// padTravel is how far the handle's center travels across the pad.
	padTravel = padSize - handleSize
)

var (
	padFill       = ui.RGB(255, 255, 255)
	padGrid       = ui.Hex("#eceef0")
	padBorder     = ui.Hex("#d1d9e0")
	padHandleEdge = ui.RGBA(128, 128, 128, 0.4)
	padShadow     = ui.RGBA(0, 0, 0, 0.18)
)

// Reset reads the settings into the pad's state, when the window opens.
func (u *UI) Reset() {
	u.padX, u.padY = DefaultOffset, DefaultOffset
	u.padChoice = primaryOption
	if u.Store != nil {
		u.padX, u.padY = u.Store.OffsetX, u.Store.OffsetY
		if u.Store.ScreenLabel != "" && u.Displays != nil {
			for _, d := range u.Displays() {
				if d.Label == u.Store.ScreenLabel {
					u.padChoice = d.Label
					break
				}
			}
		}
		u.lastAlwaysOnTop = u.Store.AlwaysOnTop
	}
	u.padPressed = false
	u.updateBounds()
	u.padBoundsChoice = u.padChoice
}

func (u *UI) updateBounds() {
	u.padMaxX, u.padMaxY = 0, 0
	if u.Displays == nil {
		return
	}
	for _, d := range u.Displays() {
		if (u.padChoice == primaryOption && d.Primary) || d.Label == u.padChoice {
			u.padMaxX, u.padMaxY = float64(d.WorkArea.Width), float64(d.WorkArea.Height)
			return
		}
	}
}

func (u *UI) screenLabel() string {
	if u.padChoice == primaryOption {
		return ""
	}
	return u.padChoice
}

// syncBounds follows the picker: after a frame chose another screen, the
// offsets are measured against that screen and brought into its work area.
func (u *UI) syncBounds() {
	if u.padChoice == u.padBoundsChoice {
		if u.padMaxX > 0 || u.padMaxY > 0 {
			return
		}
		// The screens were not known when the bounds were measured; a
		// window may open before the display list is ready.
		u.updateBounds()
		u.padX = clampOffset(u.padX, u.padMaxX)
		u.padY = clampOffset(u.padY, u.padMaxY)
		return
	}
	u.updateBounds()
	u.padX = clampOffset(u.padX, u.padMaxX)
	u.padY = clampOffset(u.padY, u.padMaxY)
	u.padBoundsChoice = u.padChoice
}

// clampOffset brings an offset into 0..limit, keeping it when the work area
// is not known: an unknown screen must not throw the position away.
func clampOffset(v, limit float64) float64 {
	if limit <= 0 {
		return v
	}
	return min(max(v, 0), limit)
}

// View builds the settings interface.
func (u *UI) View(c *ui.Context) {
	t := c.Theme()

	// Reset fills the pad from the settings when the window opens; a view
	// built first, such as a test's, starts from them too.
	if u.padBoundsChoice == "" {
		u.Reset()
	}

	options := []string{primaryOption}
	if u.Displays != nil {
		for _, d := range u.Displays() {
			options = append(options, d.Label)
		}
	}
	// A screen may have gone away since the window opened.
	if !slices.Contains(options, u.padChoice) {
		u.padChoice = primaryOption
	}
	u.syncBounds()

	ui.Column(c).Fill().Padding(16).Gap(16).Children(func() {
		u.imageSection(c, t)
		ui.Divider(c)
		u.windowSection(c, t)
		ui.Divider(c)
		u.positionSection(c, t, options)
		ui.Spacer(c)
		ui.Row(c).Children(func() {
			ui.Spacer(c)
			if ui.PrimaryButton(c, "Apply").Clicked() && u.Apply != nil {
				u.Apply(u.screenLabel(), u.padX, u.padY)
			}
		})
	})
}

// imageSection shows the character image, with the buttons that pick
// another one or go back to the bundled character.
func (u *UI) imageSection(c *ui.Context, t *ui.Theme) {
	ui.Column(c).Gap(8).Children(func() {
		ui.Text(c, "Character Image").Bold()
		ui.Row(c).Gap(8).Children(func() {
			var bitmap *ui.Bitmap
			if u.Bitmap != nil {
				bitmap = u.Bitmap()
			}
			if bitmap != nil {
				ui.Image(c, bitmap).Size(60, 60)
			} else {
				ui.Box(c).Size(60, 60).Center().Children(func() {
					ui.Text(c, "No image").FontSize(11).TextColor(t.TextMuted)
				})
			}
			if ui.Button(c, "Choose Image…").Clicked() && u.PickImage != nil {
				u.PickImage()
			}
			if ui.Button(c, "Remove").Clicked() && u.RemoveImage != nil {
				u.RemoveImage()
			}
		})
	})
}

// windowSection holds the always-on-top toggle, applied as it changes as
// the Swift toggle did.
func (u *UI) windowSection(c *ui.Context, t *ui.Theme) {
	ui.Column(c).Gap(8).Children(func() {
		ui.Text(c, "Window").Bold()
		if u.Store == nil {
			ui.Text(c, "Settings unavailable").FontSize(12).TextColor(t.TextMuted)
			return
		}
		ui.Checkbox(c, &u.Store.AlwaysOnTop, "Always on Top")
		if u.Store.AlwaysOnTop != u.lastAlwaysOnTop {
			u.lastAlwaysOnTop = u.Store.AlwaysOnTop
			if u.ApplyAlwaysOnTop != nil {
				u.ApplyAlwaysOnTop(u.Store.AlwaysOnTop)
			}
		}
	})
}

// positionSection picks the screen and sets the offsets from its
// bottom-right corner with the coordinate pad.
func (u *UI) positionSection(c *ui.Context, t *ui.Theme, options []string) {
	ui.Column(c).Gap(8).Children(func() {
		ui.Text(c, "Position").Bold()
		ui.Select(c, &u.padChoice, options).Label("Screen")
		ui.Row(c).Gap(12).AlignItems(ui.Start).Children(func() {
			u.coordinatePad(c)
			ui.Column(c).Gap(4).Children(func() {
				ui.Text(c, "Offset from").FontSize(12).TextColor(t.TextMuted)
				ui.Text(c, "bottom-right corner").FontSize(12).TextColor(t.TextMuted)
				ui.Box(c).Height(8)
				ui.Text(c, fmt.Sprintf("x: %.0f", u.padX)).FontSize(12).Font("monospace")
				ui.Text(c, fmt.Sprintf("y: %.0f", u.padY)).FontSize(12).Font("monospace")
			})
		})
	})
}

// coordinatePad draws the 256×256 pad whose origin is the bottom-right
// corner, and moves its handle: a click jumps the handle to the pointer,
// and a drag moves it by the pointer's motion.
func (u *UI) coordinatePad(c *ui.Context) {
	// The pad keeps its size even in a window squeezed to its minimum: the
	// pointer's position means DIPs of a 256×256 pad.
	pad := ui.Box(c).Size(padSize, padSize).Shrink(0)
	pad.Draw(func(p *ui.Painter, r ui.Rect) {
		// White background, as the Swift canvas had.
		p.Fill(ui.Rect{X: r.X, Y: r.Y, W: r.W, H: r.H}, padFill, 0)
		// Grid every 32 DIPs, not on the edges.
		for x := float32(gridStep); x < r.W; x += gridStep {
			p.Line(r.X+x, r.Y, r.X+x, r.Y+r.H, 1, padGrid)
		}
		for y := float32(gridStep); y < r.H; y += gridStep {
			p.Line(r.X, r.Y+y, r.X+r.W, r.Y+y, 1, padGrid)
		}
		// Border inside the edge.
		p.Stroke(ui.Rect{X: r.X, Y: r.Y, W: r.W, H: r.H}, padBorder, 0, 1.5)

		// Metallic handle: a white circle with a gray edge and a soft
		// shadow under it.
		cx, cy := u.handleCenter(r)
		handle := ui.Rect{X: cx - handleSize/2, Y: cy - handleSize/2, W: handleSize, H: handleSize}
		p.Shadow(handle, handleSize/2, 0.5, 1, 1.5, 0, padShadow)
		p.Fill(handle, padFill, handleSize/2)
		p.Stroke(handle, padHandleEdge, handleSize/2, 0.5)
	})

	// While the pad is pressed, Dragged reports the pointer's motion since
	// the last frame; the first frame jumps the handle to the pointer.
	if dx, dy, pressed := pad.Dragged(); pressed {
		if !u.padPressed {
			u.padPressed = true
			if px, py, over := pad.PointerPosition(); over {
				u.jumpHandle(px, py)
			}
		} else if dx != 0 || dy != 0 {
			u.padX = clampOffset(u.padX-float64(dx)/padTravel*u.padMaxX, u.padMaxX)
			u.padY = clampOffset(u.padY-float64(dy)/padTravel*u.padMaxY, u.padMaxY)
		}
	} else {
		u.padPressed = false
	}
}

// handleCenter returns the center of the handle in the pad's box, for the
// offsets it shows. The origin is the bottom-right corner.
func (u *UI) handleCenter(r ui.Rect) (x, y float32) {
	hh := float32(handleSize / 2)
	ratioX, ratioY := float32(0), float32(0)
	if u.padMaxX > 0 {
		ratioX = float32(min(max(u.padX/u.padMaxX, 0), 1))
	}
	if u.padMaxY > 0 {
		ratioY = float32(min(max(u.padY/u.padMaxY, 0), 1))
	}
	return r.X + hh + (r.W-handleSize)*(1-ratioX),
		r.Y + hh + (r.H-handleSize)*(1-ratioY)
}

// jumpHandle moves the offsets to where in the pad the pointer pressed (px,
// py are in the pad's coordinates): the position its center would travel
// to, with the handle kept inside the pad. An axis whose work area is not
// known keeps its offset.
func (u *UI) jumpHandle(px, py float32) {
	hh := float32(handleSize / 2)
	cx := min(max(px, hh), padSize-hh)
	cy := min(max(py, hh), padSize-hh)
	if u.padMaxX > 0 {
		u.padX = clampOffset(float64((padSize-hh-cx)/padTravel)*u.padMaxX, u.padMaxX)
	}
	if u.padMaxY > 0 {
		u.padY = clampOffset(float64((padSize-hh-cy)/padTravel)*u.padMaxY, u.padMaxY)
	}
}
