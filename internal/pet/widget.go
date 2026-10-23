package pet

import (
	"math"
	"time"

	"github.com/egoist/mygo/ui"
)

// The rod and the base, as Swift drew them: gray at half and at two
// fifths opacity.
var (
	rodColor  = ui.RGBA(128, 128, 128, 0.5)
	baseColor = ui.RGBA(128, 128, 128, 0.4)
)

const (
	// physicsStep is the fixed simulation step: the spring coefficients
	// are tuned for 100 Hz, and stepping in these slices keeps the same
	// trajectory when the widget draws less often.
	physicsStep = 10 * time.Millisecond

	// renderInterval is the fastest the widget asks for another frame
	// while the spring settles.
	renderInterval = time.Second / 60

	// maxCatchUp is the most physics slices one frame may run after a
	// pause, so a long pause does not fast-forward the sway.
	maxCatchUp = 3
)

// Widget draws the character on its rod over a transparent window, and
// swings it as the pointer pulls.
//
// It is the Swift widget's SakanaWidgetView, its animator and its viewport
// layout in one: View builds a frame from the spring's state. While the
// spring settles, View draws at most 60 fps and steps the spring in fixed
// 100 Hz slices, so the sway's motion and its ~10 s duration stay the same.
type Widget struct {
	bitmap   *ui.Bitmap
	metrics  Metrics
	state    *State
	counters counters

	// nativeSway is set while Core Animation owns the launch sway: View
	// draws no frames of its own until it is cleared.
	nativeSway bool
	// NativeInterrupt cancels the native sway when the pointer grabs the
	// character. It runs on the main thread.
	NativeInterrupt func()

	last time.Time
	// accum is unstepped time, drained in physicsStep slices by step.
	accum time.Duration
	// dragging is whether the pointer is pulling the character, and dragX
	// and dragY how far it has pulled it from where the press began.
	dragging     bool
	dragX, dragY float64
}

// NewWidget returns a widget showing b, kicked off so it greets the user
// with a sway.
func NewWidget(b *ui.Bitmap) *Widget {
	w := &Widget{bitmap: b}
	w.SetBitmap(b)
	return w
}

// Bitmap returns the character image.
func (w *Widget) Bitmap() *ui.Bitmap { return w.bitmap }

// State returns the spring, for the app and for tests.
func (w *Widget) State() *State { return w.state }

// SetNativeSway tells View that Core Animation is drawing the sway.
func (w *Widget) SetNativeSway(on bool) { w.nativeSway = on }

// Metrics returns the viewport the current character needs.
func (w *Widget) Metrics() Metrics { return w.metrics }

// SetBitmap shows b instead of the current character, and re-kicks the
// spring as changing the image did.
func (w *Widget) SetBitmap(b *ui.Bitmap) {
	w.bitmap = b
	size := 120.0
	if b != nil {
		bw, bh := b.Size()
		size = PreferredImageSize(bw, bh)
	}
	w.metrics = MetricsFor(size)
	w.state = NewState(size)
	w.state.Kick()
	w.last = time.Time{}
	w.accum = 0
	w.dragging = false
	w.dragX, w.dragY = 0, 0
}

// Drag pulls the character by dx, dy DIPs from where the press began.
func (w *Widget) Drag(dx, dy float64) {
	w.state.Drag(dx, dy)
}

// View builds one frame. It is the Swift widget's body and its display
// link's tick: a press and drag set the spring's swing, and between drags
// time advances it, until it comes to rest and no frames are asked for.
func (w *Widget) View(c *ui.Context) {
	start := time.Now()
	now := c.Now()
	// Mygo may run the view more than once for one frame, when a pass
	// consumes input; the frame is the first pass, whose time differs from
	// the previous frame's.
	if w.last.IsZero() || now.After(w.last) {
		w.counters.frame(start)
	}
	defer func() { w.counters.built(time.Since(start)) }()

	var dt time.Duration
	if !w.last.IsZero() {
		dt = now.Sub(w.last)
	}
	w.last = now

	// The window is transparent: everything but the rod, the base and the
	// character must show what is behind it.
	c.Root().Background(ui.Transparent)

	// The full-window surface takes the pointer and paints the rod and the
	// base; the character is its child, drawn above them.
	surface := ui.Box(c).Fill()
	dx, dy, pressed := surface.Dragged()
	if pressed {
		if w.nativeSway {
			w.nativeSway = false
			if w.NativeInterrupt != nil {
				w.NativeInterrupt()
			}
		}
		if !w.dragging {
			// The first frame of a press starts the drag where it began,
			// as SwiftUI's DragGesture translation did.
			w.dragging = true
			w.dragX, w.dragY = 0, 0
			w.accum = 0
		}
		w.dragX += float64(dx)
		w.dragY += float64(dy)
		w.state.Drag(w.dragX, w.dragY)
	} else {
		// Released: the spring takes over from where the drag left it. A
		// spring already at rest stays where it is: a frame that only
		// redraws the pet, as changing the image or a setting does, would
		// otherwise step its last, imperceptible motion back into a sway.
		w.dragging = false
		if !w.state.Resting() && !w.nativeSway {
			w.step(dt)
		}
	}

	m := w.metrics
	midX := m.MidX()
	baseTopY := m.BaseTopY()
	rad := w.state.R * math.Pi / 180
	attachX := float32(midX + math.Sin(rad)*m.RodLength)
	attachY := float32(baseTopY - math.Cos(rad)*m.RodLength + w.state.Y*m.RodVerticalScale)

	// Draw paints on the window, in DIPs, and may run more than once a
	// frame, so it only paints what the frame's numbers say.
	surface.Draw(func(p *ui.Painter, _ ui.Rect) {
		var rod ui.Path
		rod.MoveTo(float32(midX), float32(baseTopY))
		rod.LineTo(attachX, attachY)
		// Swift stroked the rod with a butt cap; Mygo strokes paths with
		// round ones. At 2 DIPs the ends are hidden by the base and the
		// character, so the two look the same.
		p.StrokePath(&rod, 2, rodColor)
		p.Fill(ui.Rect{
			X: float32(midX - m.BaseWidth/2),
			Y: float32(baseTopY),
			W: float32(m.BaseWidth),
			H: float32(m.BaseHeight),
		}, baseColor, 4)
	})

	if w.bitmap != nil {
		s := float32(m.ImageSize)
		surface.Children(func() {
			// The character's bottom center hangs from the rod's end, and
			// it turns around that point, as rotationEffect(anchor: .bottom)
			// did. PassThrough lets a press on the character reach the
			// surface that drags it: a plain image would take the pointer.
			ui.Image(c, w.bitmap).
				Size(s, s).
				Absolute().
				Left(attachX-s/2).
				Top(attachY-s).
				TransformOrigin(0.5, 1).
				Rotate(float32(w.state.R)).
				PassThrough()
		})
	}

	// Frames only while something moves: Mygo idles at 0 fps at rest. A
	// drag keeps the display rate for a responsive pull.
	switch {
	case w.nativeSway:
		w.counters.active()
	case w.dragging:
		w.counters.active()
		c.AnimationFrame()
	case !w.state.Resting():
		w.counters.active()
		c.After(renderInterval)
	default:
		w.accum = 0
		w.counters.idle(start)
	}
}

// step advances the spring in fixed physicsStep slices, draining up to
// maxCatchUp slices per frame.
func (w *Widget) step(dt time.Duration) {
	if dt <= 0 {
		return
	}
	w.accum += dt
	if w.accum > maxCatchUp*physicsStep {
		w.accum = maxCatchUp * physicsStep
	}
	for w.accum >= physicsStep && !w.state.Resting() {
		w.state.Step(physicsStep)
		w.accum -= physicsStep
	}
	if w.state.Resting() {
		w.accum = 0
	}
}
