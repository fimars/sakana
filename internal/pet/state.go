package pet

import "time"

// State is the spring that swings the character: the Swift SakanaState,
// ported unchanged, so the widget sways as it did.
//
// R is the rod's angle in degrees, Y how far it bends down, and T and W
// their velocities; Size is the character's size in DIPs, which caps the
// swing.
type State struct {
	Inertia    float64
	Stickiness float64
	Decay      float64
	Size       float64

	R float64
	Y float64
	T float64
	W float64
}

// NewState returns a resting spring for a character size DIPs large.
func NewState(size float64) *State {
	return &State{Inertia: 0.08, Stickiness: 0.1, Decay: 0.99, Size: size}
}

// MaxR is the largest angle the rod swings to, in degrees.
func (s *State) MaxR() float64 { return clamp(s.Size/5, 30, 60) }

// MaxY is the furthest the rod bends down, in DIPs.
func (s *State) MaxY() float64 { return s.Size / 4 }

// Kick starts the spring off, as the widget does when it launches or its
// character image changes.
func (s *State) Kick() {
	s.R = 15
	s.Y = 10
}

// SetSize changes the character's size and re-kicks the spring, as
// SakanaAnimator.updateSize did with a fresh state.
func (s *State) SetSize(size float64) {
	s.Size = size
	s.W, s.T = 0, 0
	s.Kick()
}

// Step advances the spring by dt. MyGo may build a frame more than once,
// with the same time: a zero dt steps not at all, so a frame moves the
// spring once.
func (s *State) Step(dt time.Duration) {
	if dt <= 0 {
		return
	}
	frameDiff := dt.Seconds() * 1000
	effInertia := s.Inertia
	if frameDiff <= 16 {
		effInertia = s.Inertia * frameDiff / 16.67
	}
	s.W -= s.R * 2
	s.R += s.W * effInertia * 1.2
	s.W *= s.Decay
	s.T -= s.Y * 2
	s.Y += s.T * effInertia * 2
	s.T *= s.Decay
}

// Drag pulls the character by dx, dy DIPs from where the press began, as
// SwiftUI's DragGesture translation did, and holds it there while the
// pointer is down.
func (s *State) Drag(dx, dy float64) {
	s.W = 0
	s.T = 0
	s.R = clamp(dx*s.Stickiness, -s.MaxR(), s.MaxR())
	s.Y = clamp(dy*s.Stickiness*2, -s.MaxY(), s.MaxY())
}

// Resting reports whether the spring has come to rest.
func (s *State) Resting() bool {
	return max(abs(s.W), abs(s.R), abs(s.T), abs(s.Y)) < 0.1
}

func clamp(v, lo, hi float64) float64 {
	return min(max(v, lo), hi)
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
