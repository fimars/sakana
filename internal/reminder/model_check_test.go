//go:build sakana_verify

// This file is Sakana's formal verification harness for the rest reminder.
// It is not part of the normal test suite: the sakana_verify build tag keeps
// it out of `go test ./...`. Run it with tools/verify-reminder.sh.
//
// It model-checks the real Scheduler. The transition relation is
// Scheduler.Apply, Stop, SetText and advance; the harness only chooses the
// interleaving of settings changes, timer deliveries and timer callbacks.
// Every interleaving up to maxDepth actions is explored, with duplicate
// states merged.
//
// The scheduler is driven with minute-long phases so no real timer can fire
// during the run; "a timer fires" is a step of the model, and "its callback
// runs" is the next one. That is the race the gen field guards: a callback
// that has fired can run after Apply or Stop has changed the cycle.
//
// Properties, in code terms:
//
//	P1 One toast request per finished work phase: an advance that moves the
//	   current cycle from Work to Rest calls show exactly once, increments
//	   shows exactly once, and no other advance calls show.
//	P2 A timer from an earlier cycle cannot touch a newer one: an advance
//	   whose gen is not the scheduler's gen leaves cfg, phase, gen, shows
//	   and the timer unchanged.
//	P3 A running cycle always has its next timer: Apply(on), Work->Rest and
//	   Rest->Work leave phase Work or Rest with a newly created timer;
//	   Stop and Apply(off) leave phase Off with no timer.
//	P4 A configuration that cannot run starts no cycle: Apply(on, 0 work)
//	   and Apply(on, 0 rest) leave phase Off, cfg.Enabled false and no
//	   timer, so normalize keeps a zero-length timer from firing forever.
package reminder

import (
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"
)

// maxDepth bounds the action sequence. The races this checks are short: the
// shortest bad order is Apply, fire, Apply, advance (four actions).
const maxDepth = 10

// The cycle the model applies. Work and rest differ so a swapped deadline
// is visible; both are long enough that no real timer fires during the run.
const (
	workLen = 40 * time.Minute
	restLen = 10 * time.Minute
)

// actionKind is one step of the model.
type actionKind int

const (
	applyOn             actionKind = iota // a settings change that starts the cycle
	applyOff                              // a settings change that turns it off
	applyUnrunnable                       // a settings change with no work length
	applyUnrunnableRest                   // a settings change with no rest length
	stopCycle                             // Stop, as quitting runs it
	setText                               // a message edit
	fireTimer                             // a live timer reaches its deadline
	runAdvance                            // the fired timer's callback takes the lock
)

// action is a step, with the generation of the timer it fires or advances.
type action struct {
	kind actionKind
	gen  uint64
}

func (a action) String() string {
	switch a.kind {
	case applyOn:
		return "Apply(reminder on, 40m work, 10m rest, message A)"
	case applyOff:
		return "Apply(reminder off)"
	case applyUnrunnable:
		return "Apply(reminder on, 0m work)"
	case applyUnrunnableRest:
		return "Apply(reminder on, 0m rest)"
	case stopCycle:
		return "Stop()"
	case setText:
		return "SetText(message B)"
	case fireTimer:
		return fmt.Sprintf("the timer of generation %d fires", a.gen)
	case runAdvance:
		return fmt.Sprintf("advance(%d) runs after taking the lock", a.gen)
	default:
		return "unknown action"
	}
}

// modelState is what the scheduler does not record: which timers are live or
// in flight, and how many toast requests show has received.
type modelState struct {
	live               int      // generation of the live timer, or -1
	fired              []uint64 // generations whose callback has not run yet
	dispatches         int      // show calls so far
	expectedDispatches int      // show calls the transitions so far require
	err                error    // a property the show callback itself broke
}

// snap is the scheduler's observable state around one action.
type snap struct {
	enabled    bool
	work, rest time.Duration
	text       string
	phase      Phase
	gen        uint64
	shows      uint64
	until      time.Time
	timer      *time.Timer
}

func snapshotOf(s *Scheduler) snap {
	return snap{
		enabled: s.cfg.Enabled,
		work:    s.cfg.Work,
		rest:    s.cfg.Rest,
		text:    s.cfg.Text,
		phase:   s.phase,
		gen:     s.gen,
		shows:   s.shows,
		until:   s.until,
		timer:   s.timer,
	}
}

func (a snap) sameAs(b snap) bool {
	return a.enabled == b.enabled && a.work == b.work && a.rest == b.rest &&
		a.text == b.text && a.phase == b.phase && a.gen == b.gen &&
		a.shows == b.shows && a.timer == b.timer
}

// replay builds a fresh Scheduler and applies path to it, checking every
// step. It returns the scheduler and the model state at the end.
func replay(path []action) (*Scheduler, *modelState, error) {
	ms := &modelState{live: -1}
	var s *Scheduler
	s = New(func(text string) {
		ms.dispatches++
		if !s.cfg.Enabled || s.phase != Rest {
			ms.err = fmt.Errorf(
				"show was called while the cycle is phase=%s, enabled=%t; a toast request must come from a finished work phase of a running cycle",
				s.phase, s.cfg.Enabled)
		}
		if text != s.cfg.Text {
			ms.err = fmt.Errorf("show was called with %q but the cycle's message is %q", text, s.cfg.Text)
		}
	})
	for i := range path {
		if err := step(s, ms, path[i]); err != nil {
			return nil, nil, fmt.Errorf("%s\n  after step %d: %w", trace(path[:i+1]), i+1, err)
		}
	}
	return s, ms, nil
}

// step applies one action and checks the properties it must keep.
func step(s *Scheduler, ms *modelState, a action) error {
	started := time.Now()
	before := snapshotOf(s)
	beforeTimer := s.timer
	beforeDispatches := ms.dispatches

	switch a.kind {
	case applyOn:
		s.Apply(Config{Enabled: true, Work: workLen, Rest: restLen, Text: "A"})
		ms.live = int(s.gen)
		if err := expectSnapshot(s, action{kind: applyOn}, before, snap{
			enabled: true, work: workLen, rest: restLen, text: "A",
			phase: Work, gen: before.gen + 1, shows: 0,
		}); err != nil {
			return err
		}
		if err := expectDeadline(s, started, workLen, "Apply(on)"); err != nil {
			return err
		}

	case applyOff:
		s.Apply(Config{Enabled: false, Work: workLen, Rest: restLen, Text: "A"})
		ms.live = -1
		if err := expectSnapshot(s, action{kind: applyOff}, before, snap{
			enabled: false, work: workLen, rest: restLen, text: "A",
			phase: Off, gen: before.gen + 1, shows: 0,
		}); err != nil {
			return err
		}
		if !s.until.IsZero() {
			return fmt.Errorf("Apply(off) left a deadline installed")
		}

	case applyUnrunnable:
		// normalizer must turn this into a stopped cycle rather than a
		// zero-length timer that fires at once and again forever.
		s.Apply(Config{Enabled: true, Work: 0, Rest: restLen, Text: "A"})
		ms.live = -1
		if err := expectSnapshot(s, action{kind: applyUnrunnable}, before, snap{
			enabled: false, work: 0, rest: restLen, text: "A",
			phase: Off, gen: before.gen + 1, shows: 0,
		}); err != nil {
			return err
		}
		if !s.until.IsZero() {
			return fmt.Errorf("Apply(on, 0m work) left a deadline installed")
		}

	case applyUnrunnableRest:
		// The same guard covers a zero rest length: no zero-length rest
		// timer either.
		s.Apply(Config{Enabled: true, Work: workLen, Rest: 0, Text: "A"})
		ms.live = -1
		if err := expectSnapshot(s, action{kind: applyUnrunnableRest}, before, snap{
			enabled: false, work: workLen, rest: 0, text: "A",
			phase: Off, gen: before.gen + 1, shows: 0,
		}); err != nil {
			return err
		}
		if !s.until.IsZero() {
			return fmt.Errorf("Apply(on, 0m rest) left a deadline installed")
		}

	case stopCycle:
		s.Stop()
		ms.live = -1
		want := before
		want.phase, want.gen, want.shows, want.timer = Off, before.gen+1, 0, nil
		if err := expectSnapshot(s, action{kind: stopCycle}, before, want); err != nil {
			return err
		}
		if !s.until.IsZero() {
			return fmt.Errorf("Stop left a deadline installed")
		}

	case setText:
		s.SetText("B")
		want := before
		want.text = "B"
		if err := expectSnapshot(s, action{kind: setText}, before, want); err != nil {
			return err
		}

	case fireTimer:
		if ms.live < 0 || a.gen != uint64(ms.live) {
			return fmt.Errorf("fired generation %d is not the live timer %d", a.gen, ms.live)
		}
		ms.live = -1
		ms.fired = append(ms.fired, a.gen)
		if !sameSnapshot(s, before) {
			return fmt.Errorf("delivering the timer changed the scheduler state")
		}
		if !s.until.Equal(before.until) {
			return fmt.Errorf("delivering a timer changed the phase deadline")
		}

	case runAdvance:
		i := indexOf(ms.fired, a.gen)
		if i < 0 {
			return fmt.Errorf("advance(%d) has no fired callback in flight %v", a.gen, ms.fired)
		}
		ms.fired = append(ms.fired[:i], ms.fired[i+1:]...)
		s.advance(a.gen)

		switch {
		case a.gen != s.gen || !s.cfg.Enabled:
			// P2: a callback from an old cycle, or one that runs while the
			// cycle is off, must leave everything as it was.
			if !before.sameAs(snapshotOf(s)) {
				return fmt.Errorf(
					"P2 broken: advance(%d) from an earlier cycle changed the current cycle (phase %s -> %s, shows %d -> %d, gen %d -> %d)",
					a.gen, before.phase, s.phase, before.shows, s.shows, before.gen, s.gen)
			}
			if ms.dispatches != beforeDispatches {
				return fmt.Errorf("P2 broken: a stale advance(%d) called show", a.gen)
			}
			if !s.until.Equal(before.until) {
				return fmt.Errorf("P2 broken: a stale advance(%d) changed the next timer's deadline", a.gen)
			}

		case before.phase == Work:
			// P1: the finished work phase shows exactly one toast and
			// rests, with the next timer in place.
			if s.phase != Rest || s.shows != before.shows+1 {
				return fmt.Errorf(
					"P1 broken: a work phase ended and advance(%d) left phase=%s shows=%d, want phase=rest shows=%d",
					a.gen, s.phase, s.shows, before.shows+1)
			}
			if ms.dispatches != beforeDispatches+1 {
				return fmt.Errorf("P1 broken: a finished work phase did not call show exactly once")
			}
			if err := expectDeadline(s, started, restLen, "a finished work phase"); err != nil {
				return err
			}
			ms.expectedDispatches++
			ms.live = int(s.gen)

		case before.phase == Rest:
			// The rest phase ended: work starts again and no toast shows.
			if s.phase != Work || s.shows != before.shows {
				return fmt.Errorf(
					"P1/P3 broken: a rest phase ended and advance(%d) left phase=%s shows=%d",
					a.gen, s.phase, s.shows)
			}
			if err := expectDeadline(s, started, workLen, "a finished rest phase"); err != nil {
				return err
			}
			ms.live = int(s.gen)

		default:
			// A current-generation timer with the cycle off: nothing to do,
			// but it must not touch the state.
			if !before.sameAs(snapshotOf(s)) {
				return fmt.Errorf("advance(%d) changed an off cycle", a.gen)
			}
			if !s.until.Equal(before.until) {
				return fmt.Errorf("advance(%d) changed an off cycle's deadline", a.gen)
			}
			ms.live = -1
		}
	}

	// Keep the real timer out of the runtime's way: the model fires timers,
	// not the clock.
	if s.timer != nil {
		s.timer.Stop()
	}

	if err := checkState(s, ms, before, beforeTimer, a); err != nil {
		return err
	}
	return ms.err
}

// checkState holds for every state after an action.
func checkState(s *Scheduler, ms *modelState, before snap, beforeTimer *time.Timer, a action) error {
	after := snapshotOf(s)

	// P3: an off cycle has no timer; a running one has a timer.
	if after.phase == Off && after.timer != nil {
		return fmt.Errorf("P3 broken: phase is off but a timer is still installed")
	}
	if after.phase != Off && after.timer == nil {
		return fmt.Errorf("P3 broken: phase is %s but no timer is installed, so the cycle would stall", after.phase)
	}

	// P3: the actions that start a phase must install a fresh timer, so a
	// removed startLocked assignment cannot hide behind the old pointer.
	startsPhase := a.kind == applyOn ||
		(a.kind == runAdvance && a.gen == s.gen && s.cfg.Enabled && before.phase != Off)
	if startsPhase {
		if after.timer == nil || after.timer == beforeTimer {
			return fmt.Errorf("P3 broken: %s left the previous timer in place instead of scheduling the next phase", a)
		}
	} else if a.kind == setText || a.kind == fireTimer || a.kind == runAdvance {
		// Actions that do not start a phase leave the live timer alone.
		if after.timer != beforeTimer {
			return fmt.Errorf("P3 broken: %s replaced or removed the live timer without starting a phase", a)
		}
	}

	// The model's own bookkeeping must stay consistent with the scheduler.
	if ms.live >= 0 && uint64(ms.live) != s.gen {
		return fmt.Errorf("model error: live timer generation %d is not the scheduler's %d", ms.live, s.gen)
	}
	for i, g := range ms.fired {
		if g > s.gen {
			return fmt.Errorf("model error: in-flight generation %d is newer than the scheduler's %d", g, s.gen)
		}
		if j := indexOf(ms.fired, g); j != i {
			return fmt.Errorf("model error: generation %d is in flight twice", g)
		}
	}
	if ms.dispatches != ms.expectedDispatches {
		return fmt.Errorf(
			"P1 broken: %d show calls so far, but the transitions require %d",
			ms.dispatches, ms.expectedDispatches)
	}
	return nil
}

// expectSnapshot compares the scheduler with want, ignoring the timer pointer
// (checkState handles that).
func expectSnapshot(s *Scheduler, a action, before, want snap) error {
	got := snapshotOf(s)
	got.timer = want.timer
	if !got.sameAs(want) {
		return fmt.Errorf(
			"%s left enabled=%t work=%v rest=%v text=%q phase=%s gen=%d shows=%d; want enabled=%t work=%v rest=%v text=%q phase=%s gen=%d shows=%d",
			a, got.enabled, got.work, got.rest, got.text, got.phase, got.gen, got.shows,
			want.enabled, want.work, want.rest, want.text, want.phase, want.gen, want.shows)
	}
	return nil
}

// expectDeadline checks the phase the action started runs for d from when
// the action began, so work and rest lengths cannot be swapped or dropped.
func expectDeadline(s *Scheduler, started time.Time, d time.Duration, what string) error {
	if s.until.IsZero() {
		return fmt.Errorf("%s installed no phase deadline; want %v from now", what, d)
	}
	got := s.until.Sub(started)
	if got < d || got > d+5*time.Second {
		return fmt.Errorf("%s scheduled the next phase in %v, want %v", what, got, d)
	}
	return nil
}

func sameSnapshot(s *Scheduler, before snap) bool {
	return before.sameAs(snapshotOf(s))
}

func indexOf(gs []uint64, g uint64) int {
	for i, x := range gs {
		if x == g {
			return i
		}
	}
	return -1
}

// candidates lists the actions enabled in a state.
func candidates(ms *modelState) []action {
	out := []action{
		{kind: applyOn},
		{kind: applyOff},
		{kind: applyUnrunnable},
		{kind: applyUnrunnableRest},
		{kind: stopCycle},
		{kind: setText},
	}
	if ms.live >= 0 {
		out = append(out, action{kind: fireTimer, gen: uint64(ms.live)})
	}
	for _, g := range ms.fired {
		out = append(out, action{kind: runAdvance, gen: g})
	}
	return out
}

// stateKey merges states that differ only in absolute generation numbers.
func stateKey(s *Scheduler, ms *modelState) string {
	live := -1
	if ms.live >= 0 {
		live = int(s.gen) - ms.live
	}
	ages := make([]int, len(ms.fired))
	for i, g := range ms.fired {
		ages[i] = int(s.gen) - int(g)
	}
	sort.Ints(ages)
	return fmt.Sprintf("enabled=%t text=%q phase=%s shows=%d liveAge=%d firedAges=%v dispatches=%d",
		s.cfg.Enabled, s.cfg.Text, s.phase, s.shows, live, ages, ms.dispatches)
}

// trace renders a counterexample as numbered steps in the code's words.
func trace(path []action) string {
	var b strings.Builder
	b.WriteString("counterexample:")
	for i, a := range path {
		fmt.Fprintf(&b, "\n  %d. %s", i+1, a)
	}
	return b.String()
}

// TestSchedulerModelCheck explores every action interleaving up to maxDepth
// and fails on the first one that breaks P1-P4.
func TestSchedulerModelCheck(t *testing.T) {
	// Breadth first, so a counterexample is the shortest sequence of steps
	// that reaches the break.
	queue := [][]action{nil}
	seen := make(map[string]bool)
	states, transitions := 0, 0

	for len(queue) > 0 {
		path := queue[0]
		queue = queue[1:]

		s, ms, err := replay(path)
		if err != nil {
			t.Fatalf("model check failed:\n%v", err)
		}
		key := stateKey(s, ms)
		if seen[key] {
			continue
		}
		seen[key] = true
		states++
		if len(path) >= maxDepth {
			continue
		}
		for _, a := range candidates(ms) {
			transitions++
			queue = append(queue, append(append([]action(nil), path...), a))
		}
	}
	t.Logf("P1-P4 hold over %d states and %d transitions up to %d actions", states, transitions, maxDepth)
}
