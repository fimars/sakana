//go:build !darwin

package ca

import (
	"errors"
	"time"
)

// Frame is one keyframe of the sway: the rod's angle in degrees and how
// far it has bent down, in DIPs.
type Frame struct {
	R float64
	Y float64
}

// Metrics is the viewport geometry the overlay draws in, in DIPs: the same
// numbers the pet widget lays its rod, base and character out with.
type Metrics struct {
	MidX, BaseTopY, BaseWidth, BaseHeight, RodLength, RodVerticalScale, ImageSize float64
}

// Sway is one running overlay. There is no overlay off darwin.
type Sway struct{}

// closedDone is what Done returns: the keyframes never run, so the wait
// ends at once.
var closedDone = func() chan struct{} {
	c := make(chan struct{})
	close(c)
	return c
}()

// Start returns an error: the overlay needs darwin.
func Start(win uintptr, image []byte, m Metrics, frames []Frame, interval time.Duration) (*Sway, error) {
	return nil, errors.New("ca: the overlay needs darwin")
}

// Presentation reports no pose: there is no overlay off darwin.
func (s *Sway) Presentation() (r, x, y float64, ok bool) { return 0, 0, 0, false }

// Done is closed at once: the keyframes never run off darwin.
func (s *Sway) Done() <-chan struct{} { return closedDone }

// Cancel is a no-op: there is no overlay to remove.
func (s *Sway) Cancel() {}
