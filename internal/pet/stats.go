package pet

import (
	"sync/atomic"
	"time"
)

// FrameStats is what the widget's frames cost: how many it built, when it
// built the first and the last, and how long building them took, without
// the paint that follows. IdleAt is when the last frame asked for no
// further frame, so that the widget came to rest.
type FrameStats struct {
	Frames     int64
	FirstFrame time.Time
	LastFrame  time.Time
	IdleAt     time.Time
	BuildTotal time.Duration
	BuildMax   time.Duration
}

// counters holds the widget's frame telemetry. View runs on the main
// thread and adds to it; Stats reads it from the benchmark harness.
type counters struct {
	frames     atomic.Int64
	firstFrame atomic.Int64 // unix nanoseconds
	lastFrame  atomic.Int64 // unix nanoseconds
	idleAt     atomic.Int64 // unix nanoseconds
	buildTotal atomic.Int64 // nanoseconds
	buildMax   atomic.Int64 // nanoseconds
}

// frame records a frame built at start.
func (c *counters) frame(start time.Time) {
	c.frames.Add(1)
	at := start.UnixNano()
	c.firstFrame.CompareAndSwap(0, at)
	c.lastFrame.Store(at)
}

// active records a frame that asks for another: the widget moves.
func (c *counters) active() { c.idleAt.Store(0) }

// idle records the first frame that asks for no other: the widget rests.
func (c *counters) idle(start time.Time) { c.idleAt.CompareAndSwap(0, start.UnixNano()) }

// built records how long a frame took to build.
func (c *counters) built(d time.Duration) {
	c.buildTotal.Add(int64(d))
	for {
		max := c.buildMax.Load()
		if int64(d) <= max || c.buildMax.CompareAndSwap(max, int64(d)) {
			return
		}
	}
}

func (c *counters) load() FrameStats {
	var s FrameStats
	s.Frames = c.frames.Load()
	if ns := c.firstFrame.Load(); ns != 0 {
		s.FirstFrame = time.Unix(0, ns)
	}
	if ns := c.lastFrame.Load(); ns != 0 {
		s.LastFrame = time.Unix(0, ns)
	}
	if ns := c.idleAt.Load(); ns != 0 {
		s.IdleAt = time.Unix(0, ns)
	}
	s.BuildTotal = time.Duration(c.buildTotal.Load())
	s.BuildMax = time.Duration(c.buildMax.Load())
	return s
}

// Stats returns the frames the widget has built, for the benchmark harness.
func (w *Widget) Stats() FrameStats { return w.counters.load() }
