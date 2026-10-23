---
name: verify-sakana-reminder
description: "Exhaustively model-check Sakana's rest-reminder scheduler (internal/reminder/scheduler.go): the work/rest cycle, the timer generation guard, toast requests, Apply/Stop/SetText and normalize. Use for /verify-sakana-reminder, before finishing any change to the reminder or its timer handling, or after a reminder bug where toasts appear at the wrong time or stop."
---

# Verify Sakana's rest reminder

A work phase ends, a toast shows, a rest phase runs, work resumes. This check
proves the scheduler keeps that promise for every order of settings changes,
timer deliveries and timer callbacks up to 10 actions. It drives the real
`Scheduler` in `internal/reminder/scheduler.go`, so a change to the Go code is
covered directly.

The end-to-end harness (`tools/e2e.sh`) observes one or two accelerated
cycles. The failures below need two events at nearly the same instant, such
as a work timer firing while the settings restart the cycle, so the E2E
harness cannot reach them.

## Run it when

Run the check if the change touches any of these:

- `internal/reminder/scheduler.go` at all.
- The model harness itself.

Changes to the reminder settings in `main.go` (`reminderConfig` line 331,
`applyReminder` line 345) or in the reminder section of
`internal/settings/view.go` shape the `Config` the scheduler receives. Those
functions sit outside the check (see "What a pass covers"), so a green run
does not clear them.

Run it from the repository root:

```sh
tools/verify-reminder.sh
```

It runs `go test -tags sakana_verify ./internal/reminder -run
'^TestSchedulerModelCheck$' -count=1 -v`. The build tag keeps this check out
of the normal `go test` path; the script also fails if the test did not run.

A pass prints a line like:

```
P1-P4 hold over 618 states and 3036 transitions up to 10 actions
--- PASS: TestSchedulerModelCheck
```

The state and transition counts change as the model or the scheduler
changes. The properties and the pass line matter.

## What is verified

In the code's words:

- **P1 — one toast request per finished work phase.** When `advance` moves a
  running cycle from `Work` to `Rest`, it calls `show` exactly once and
  increments `shows` exactly once. No other advance calls `show`.
- **P2 — an old timer cannot touch a newer cycle.** A callback whose `gen` is
  not the scheduler's current `gen` leaves `cfg`, `phase`, `gen`, `shows`,
  the timer and its deadline unchanged. This is the `gen != s.gen` guard in
  `advance`.
- **P3 — a running cycle always has its next timer.** `Apply` with the
  reminder on, a `Work`→`Rest` advance and a `Rest`→`Work` advance install a
  newly created timer with the right length (40 minutes of work, 10 minutes
  of rest in the model). `Stop` and an `Apply` with the reminder off leave
  `Off` with no timer and no deadline. A missing timer would stall the cycle;
  a swapped length would turn the 40/10 cycle into 10/40.
- **P4 — an unrunnable configuration starts no cycle.** `Apply` with a zero
  work or rest length leaves `Off` and `cfg.Enabled == false`, so `normalize`
  prevents a zero-length timer from firing at once and again forever.

The harm each guards against: a toast at the wrong moment, a lost reminder, a
reminder that never stops toasting, or a cycle that stops after a settings
change.

## What a pass covers

The harness `replay`s a sequence of actions against a fresh scheduler. Its
actions are the real `Apply`, `Stop`, `SetText` and `advance`; the model only
chooses the order of calls, timer deliveries and callbacks. It explores every
interleaving up to `maxDepth` (10), merging states that behave the same;
absolute generation numbers and deadlines are normalized.

The bounds and assumptions:

- One `Scheduler` and one process; `Apply` uses four configurations: on
  with 40/10 minutes and message `A`, off, on with zero work, and on with
  zero rest. `SetText` uses message `B`. Other durations and messages do not
  change these properties.
- Time is abstract: a phase is scheduled for its length and checked with
  `expectDeadline`, and a timer reaches its deadline as its own model step.
  The check does not sleep for 40 minutes.
- At most one live timer at a time, which is what `startLocked` creates.
  Several fired callbacks can be in flight and are modeled.
- The check stops at `show`. It covers the scheduler's request for a toast,
  not the main-thread delivery in `main.go`'s `reminderFired` (line 367), the
  toast window, the dismissal, or `Snapshot`'s remaining time.

## Read a failure

The failure prints a numbered counterexample in the code's words. This is the
shortest one for a removed `gen` guard:

```
counterexample:
  1. Apply(reminder on, 40m work, 10m rest, message A)
  2. the timer of generation 1 fires
  3. Apply(reminder on, 40m work, 10m rest, message A)
  4. advance(1) runs after taking the lock
  after step 4: P2 broken: advance(1) from an earlier cycle changed the
  current cycle (phase work -> rest, shows 0 -> 1, gen 2 -> 2)
```

Read it as: a settings change started cycle 1; its work timer reached its
deadline; before that callback took the lock, the settings restarted the
cycle as generation 2; the old callback then advanced the new cycle to
`Rest` and counted a toast for work that never happened. The fix is the
guard in `advance`, not a change to the check.

A failure message beginning `P1 broken`, `P2 broken`, `P3 broken` or
`P4 broken`, or a state mismatch after an action, is a finding about the
code. Report it with the counterexample. Do not weaken a property, reduce
`maxDepth`, or edit the scheduler to make the check pass unless the user asks
for that change.

A message beginning `model error:` means the harness's own bookkeeping is
inconsistent. A compile error means a field, function or action the harness
uses was renamed or removed. Both mean the harness needs updating (below);
they do not by themselves prove a product bug.

## Update the harness when the contract changes on purpose

The harness is `internal/reminder/model_check_test.go`, build tag
`sakana_verify`. Its pieces:

- `action` and `candidates` — the operations the model may schedule.
- `step` — applies one operation to the real scheduler and checks its
  expected state, counter changes and deadline.
- `snap`, `modelState`, `stateKey` — what the harness remembers between
  actions and what it uses to merge equivalent states.
- `checkState` — the checks that hold after every action.
- `TestSchedulerModelCheck` — the breadth-first search, the shortest
  counterexample and the summary line.
- `workLen`, `restLen`, `maxDepth` — the model's cycle and bound.

When the scheduler changes:

- A new field that affects the cycle goes into `snap` and `sameAs`; if it
  affects future transitions, put it in `stateKey` too.
- A new operation gets an `actionKind`, a case in `step` with its expected
  state and counter changes, and an entry in `candidates`. Add a property
  only when the operation makes a new promise.
- A changed `show` contract changes the recorder in `replay` and P1.
- A changed timer or phase rule changes the transition checks and usually
  `expectDeadline`.
- Keep `maxDepth` at least as long as the longest bad interleaving the new
  contract must survive. The `gen` race needs four actions.

Then prove the updated harness can fail: break the code on purpose, one
property at a time. Useful breaks are removing `gen != s.gen` from
`advance`, removing `s.shows++`, removing the `time.AfterFunc` assignment in
`startLocked`, removing the `s.shows = 0` in `restartLocked`, calling `show`
twice, swapping `s.cfg.Work` and `s.cfg.Rest` in the two `startLocked` calls
in `advance`, and narrowing `normalize` to one of its two length checks.
Each must produce a counterexample. Restore the code and confirm the check
passes before finishing.

## Coverage record

As of commit `0a1746f3f6c10ac5734fff07a18fc88ca22d3e22`, the check covers:

| Code (current line numbers) | What the check does |
| --- | --- |
| `Scheduler` fields `cfg`, `phase`, `gen`, `shows`, `timer`, `until` (40) | `snap`/`snapshotOf` compare them after every action |
| `New` (54) | installs the recorder that checks `show` timing and text |
| `Apply` (60), `normalize` (104), `restartLocked` (111) | `applyOn`, `applyOff`, `applyUnrunnable`, `applyUnrunnableRest`; P3, P4, `shows` reset, new generation |
| `SetText` (69) | `setText`; text changes without restarting the cycle |
| `Stop` (89) | `stopCycle`; `Off`, no timer, no deadline, `shows` reset |
| `startLocked` (128) | timer and deadline checks in `checkState`/`expectDeadline`; P3 |
| `advance` (137) | `fireTimer` then `runAdvance` with the callback's generation; P1, P2 and the phase transitions |

Not covered: `Snapshot` (76), `main.go`'s `applyReminder` (345),
`reminderFired` (367), `showToast` (382), the toast window and timers in real
time.

Before moving the sync commit forward, run the check at the new commit and
read `git diff 0a1746f -- internal/reminder/scheduler.go`. Move the hash in
this file only after confirming every row above still describes the code and
the check passes. If the scheduler changed after this commit and the harness
was not updated, a pass is stale evidence: update the harness first.
