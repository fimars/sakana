// Package reminder runs Sakana's rest reminder: a work phase, then a toast
// and a rest phase, then the next work phase, for as long as it is on.
package reminder

import (
	"sync"
	"time"
)

// Phase is where a running cycle is.
type Phase string

// The phases of the cycle; Off is a cycle that is not running.
const (
	Off  Phase = "off"
	Work Phase = "work"
	Rest Phase = "rest"
)

// Config is what the cycle runs with.
type Config struct {
	Enabled bool
	Work    time.Duration // from the start of a work phase to the toast
	Rest    time.Duration // from the toast to the next work phase
	Text    string        // what the toast says
}

// Snapshot is the cycle's state, for the app and the E2E harness.
type Snapshot struct {
	Config
	Phase     Phase
	Remaining time.Duration // of the current phase; 0 when off
	Shows     uint64        // work phases that ended in a toast since the cycle started
}

// Scheduler runs the cycle on its own clock: the app starts it with Apply,
// changes it as the settings do, and reads it with Snapshot. show runs on
// the timer's goroutine as each work phase ends, and the app brings it to
// the main thread.
type Scheduler struct {
	mu    sync.Mutex
	cfg   Config
	phase Phase
	until time.Time
	timer *time.Timer
	// gen changes with every Apply: a timer that fired just before it
	// must not advance the new cycle. shows counts the toasts it has shown.
	gen   uint64
	shows uint64
	show  func(string)
}

// New returns a scheduler that calls show as each work phase ends.
func New(show func(string)) *Scheduler {
	return &Scheduler{show: show, phase: Off}
}

// Apply sets the cycle and starts it over from a work phase, or stops it
// when it is disabled.
func (s *Scheduler) Apply(c Config) {
	c = normalize(c)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg = c
	s.restartLocked()
}

// SetText changes what the next toast says, leaving the cycle alone.
func (s *Scheduler) SetText(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg.Text = text
}

// Snapshot returns the cycle's state.
func (s *Scheduler) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := Snapshot{Config: s.cfg, Phase: s.phase, Shows: s.shows}
	if s.phase != Off && !s.until.IsZero() {
		if left := time.Until(s.until); left > 0 {
			snap.Remaining = left
		}
	}
	return snap
}

// Stop stops the cycle; the timers of a quit app would anyway never fire.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gen++
	s.shows = 0
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	s.phase = Off
	s.until = time.Time{}
}

// normalize stops a cycle whose durations cannot run, rather than let a
// zero or negative one fire at once and again forever.
func normalize(c Config) Config {
	if c.Work <= 0 || c.Rest <= 0 {
		c.Enabled = false
	}
	return c
}

func (s *Scheduler) restartLocked() {
	s.gen++
	s.shows = 0
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	if !s.cfg.Enabled {
		s.phase = Off
		s.until = time.Time{}
		return
	}
	s.startLocked(Work, s.cfg.Work)
}

// startLocked enters phase for d, scheduling the timer that leaves it.
// The caller holds s.mu.
func (s *Scheduler) startLocked(phase Phase, d time.Duration) {
	s.phase = phase
	s.until = time.Now().Add(d)
	gen := s.gen
	s.timer = time.AfterFunc(d, func() { s.advance(gen) })
}

// advance leaves the current phase: a finished work phase shows the toast
// and rests, and a finished rest phase works again.
func (s *Scheduler) advance(gen uint64) {
	s.mu.Lock()
	if gen != s.gen || !s.cfg.Enabled {
		s.mu.Unlock()
		return
	}
	switch s.phase {
	case Work:
		text, show := s.cfg.Text, s.show
		s.shows++
		s.startLocked(Rest, s.cfg.Rest)
		s.mu.Unlock()
		if show != nil {
			show(text)
		}
	case Rest:
		s.startLocked(Work, s.cfg.Work)
		s.mu.Unlock()
	default:
		s.mu.Unlock()
	}
}
