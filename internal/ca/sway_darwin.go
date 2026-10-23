//go:build darwin

// Package ca draws the pet's launch sway with Core Animation: a sampled
// keyframe trajectory handed to the window's layer tree, so the render
// server animates it while the app draws no frames.
//
// Start and Cancel must run on the main thread, as the window's layer tree
// is a main-thread object; Done may be waited on from any goroutine.
package ca

/*
#cgo CFLAGS: -fobjc-arc -mmacosx-version-min=12.0
#cgo LDFLAGS: -framework Cocoa -framework QuartzCore -framework CoreGraphics -framework ImageIO
#include <stdint.h>
#include <stddef.h>
#include <stdlib.h>

int ca_sway_start(uintptr_t win, const void *img, size_t imgLen,
    double midX, double baseTopY, double baseWidth, double baseHeight,
    double rodLength, double rodVerticalScale, double imageSize,
    const double *rs, const double *ys, int count, double duration);
void ca_sway_cancel(void);
int ca_sway_presentation(double *r, double *x, double *y);
*/
import "C"

import (
	"errors"
	"sync"
	"time"
	"unsafe"
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

// Sway is one running overlay. Start and Cancel must run on the main
// thread.
type Sway struct {
	done  chan struct{}
	timer *time.Timer
	// doneOnce makes the timer firing and Cancel idempotent against each
	// other; removeOnce takes the native overlay down exactly once, so a
	// Cancel after the keyframes ended still removes it, and a Cancel
	// after a successor's Start cannot remove the successor's overlay.
	doneOnce   sync.Once
	removeOnce sync.Once
}

// current is the one Sway whose layers are in the window. Main thread only.
var current *Sway

// Start begins the overlay on win (an NSWindow*) with image bytes decoded
// by ImageIO, using frames sampled at interval. Done closes when the
// keyframes end or Cancel runs. A second Start cancels the current overlay.
func Start(win uintptr, image []byte, m Metrics, frames []Frame, interval time.Duration) (*Sway, error) {
	switch {
	case win == 0:
		return nil, errors.New("ca: win is 0")
	case len(image) == 0:
		return nil, errors.New("ca: image is empty")
	case len(frames) < 2:
		return nil, errors.New("ca: need at least two frames")
	case interval <= 0:
		return nil, errors.New("ca: interval is not positive")
	case m.MidX <= 0 || m.BaseTopY <= 0 || m.BaseWidth <= 0 || m.BaseHeight <= 0 ||
		m.RodLength <= 0 || m.RodVerticalScale <= 0 || m.ImageSize <= 0:
		return nil, errors.New("ca: metrics are not positive")
	}
	if current != nil {
		current.Cancel()
	}
	if !startOverlay(win, image, m, frames, interval) {
		return nil, errors.New("ca: the overlay did not start")
	}
	s := &Sway{done: make(chan struct{})}
	s.timer = time.AfterFunc(interval*time.Duration(len(frames)-1), func() { s.close() })
	current = s
	return s, nil
}

// Done closes when the keyframes end or Cancel runs.
func (s *Sway) Done() <-chan struct{} { return s.done }

// Presentation reads the pose the compositor is showing for the character:
// the rotation in degrees and the attach point in DIPs. It is for the
// harness, which cannot read a presentation pose from a window capture.
func (s *Sway) Presentation() (r, x, y float64, ok bool) {
	var cr, cx, cy C.double
	if C.ca_sway_presentation(&cr, &cx, &cy) == 0 {
		return 0, 0, 0, false
	}
	return float64(cr), float64(cx), float64(cy), true
}

// Cancel stops the timer, removes the overlay and closes Done. It removes
// the overlay even after the keyframes have ended.
func (s *Sway) Cancel() {
	s.timer.Stop()
	s.removeOnce.Do(func() {
		if current == s {
			C.ca_sway_cancel()
			current = nil
		}
	})
	s.close()
}

// close closes done once, whether the timer fired first or Cancel ran.
func (s *Sway) close() { s.doneOnce.Do(func() { close(s.done) }) }

// startOverlay hands the validated keyframes to Core Animation. It is the
// cgo boundary of Start.
func startOverlay(win uintptr, image []byte, m Metrics, frames []Frame, interval time.Duration) bool {
	n := len(frames)
	rs := C.malloc(C.size_t(n) * C.size_t(unsafe.Sizeof(C.double(0))))
	ys := C.malloc(C.size_t(n) * C.size_t(unsafe.Sizeof(C.double(0))))
	if rs == nil || ys == nil {
		if rs != nil {
			C.free(rs)
		}
		if ys != nil {
			C.free(ys)
		}
		return false
	}
	defer C.free(rs)
	defer C.free(ys)

	rp := unsafe.Slice((*C.double)(rs), n)
	yp := unsafe.Slice((*C.double)(ys), n)
	for i, f := range frames {
		rp[i] = C.double(f.R)
		yp[i] = C.double(f.Y)
	}

	img := C.CBytes(image)
	if img == nil {
		return false
	}
	defer C.free(img)

	ok := C.ca_sway_start(C.uintptr_t(win), img, C.size_t(len(image)),
		C.double(m.MidX), C.double(m.BaseTopY),
		C.double(m.BaseWidth), C.double(m.BaseHeight),
		C.double(m.RodLength), C.double(m.RodVerticalScale), C.double(m.ImageSize),
		(*C.double)(rs), (*C.double)(ys), C.int(n),
		C.double((time.Duration(n-1) * interval).Seconds()))
	return ok != 0
}
