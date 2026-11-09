package host

import (
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Ports packages/chord/src/host/dependencies.ts. Parked attempts (dependency model §3, §7): the dependency check, what
// an attempt's declared dependencies (`self.get` references and `self.registry` values) say at one moment: all live, or what
// the parked attempt shows while one is not (`loading`, `failed { waiting }` naming the nearest blocker and the root cause,
// or `broken` for a cycle, authority or an ended dependency), whether this host's first pass can count it (no blocker here
// still without its first outcome, decision (a)), and its first blocker; and the `parked_long` report of a long parking
// episode.

// dependencyKind is what a dependency is.
type dependencyKind string

const (
	dependencyReference dependencyKind = "reference"
	dependencyRegistry  dependencyKind = "registry"
)

// dependency is one declared dependency: a reference (`self.get`: binding is set) or a registry's value (`self.registry`:
// value is set).
type dependency struct {
	kind    dependencyKind
	binding *binding
	value   *registries.RegistryValueSource
}

// checkKind is what the dependencies say.
type checkKind string

const (
	// checkLive: every one is live (hydrated: a reference's members have values).
	checkLive checkKind = "live"
	// checkParked: what the attempt shows (`loading`, or `failed { waiting }` naming its first failed blocker).
	checkParked checkKind = "parked"
	// checkBroken: one failed for a cycle (`invalid/cycle`), for authority (`forbidden`) or ended: it never comes back by
	// itself.
	checkBroken checkKind = "broken"
)

// blockerState is whether a blocker loads or failed.
type blockerState string

const (
	blockerLoading blockerState = "loading"
	blockerFailed  blockerState = "failed"
)

// blocker is a blocker as `parked_long` names it: a reference's label or `registry <name>`, and whether it loads or failed.
type blocker struct {
	label string
	state blockerState
}

// check is what the dependencies say, by kind. Only the fields of its kind are set. shown, firstPass, blocker and loadingOn
// are of a parked check: what the attempt shows, whether its first pass is over (no blocker, loading or failed, is an
// object here still without its first outcome), its first blocker in declaration order (`parked_long` names it), and the
// first blocker in declaration order that is only loading (a reload's report names it; nil when none). err is of a broken
// check.
type check struct {
	kind      checkKind
	shown     *RefState
	firstPass bool
	blocker   blocker
	loadingOn *string
	err       *data.ChordError
}

// checkDependencies checks dependencies in declaration order (the first failed one is the one the attempt names).
func checkDependencies(dependencies []dependency) check {
	var firstFailed *failed
	firstPass := true
	var first *blocker
	var loadingOn *string
	for _, dependency := range dependencies {
		if dependency.kind == dependencyRegistry {
			value := dependency.value
			start := value.Start()
			if start.Kind == state.StartValue || start.HasLast {
				continue
			}
			label := "registry " + value.Name
			if firstFailed == nil {
				firstFailed = waitingOn(label, data.AsChordError(start.Error, label))
			}
			if first == nil {
				first = &blocker{label: label, state: blockerFailed}
			}
			continue
		}
		binding := dependency.binding
		current := binding.refState()
		switch current.Kind {
		case RefStateLive:
			continue
		case RefStateEnded:
			return check{kind: checkBroken, err: current.Error}
		case RefStateFailed:
			err := current.Cause.Error
			if healsNever(err) {
				return check{kind: checkBroken, err: err}
			}
			if firstFailed == nil {
				firstFailed = waitingOn(binding.label(), err)
			}
			if first == nil {
				first = &blocker{label: binding.label(), state: blockerFailed}
			}
			// Parked `waiting` on something here still in its first attempt: no first outcome yet (fix round 1).
			if target := binding.target(); target != nil && target.blocksFirstPass() {
				firstPass = false
			}
		case RefStateLoading:
			if loadingOn == nil {
				label := binding.label()
				loadingOn = &label
			}
			if first == nil {
				first = &blocker{label: binding.label(), state: blockerLoading}
			}
			if target := binding.target(); target != nil && target.blocksFirstPass() {
				firstPass = false
			}
		}
	}
	if first == nil {
		return check{kind: checkLive}
	}
	shown := refLoading
	if firstFailed != nil {
		shown = firstFailed
	}
	return check{kind: checkParked, shown: shown, firstPass: firstPass, blocker: *first, loadingOn: loadingOn}
}

// healsNever reports whether a dependency that failed with err never heals by itself, so its dependent breaks too: a cycle
// (`invalid/cycle`: it would wait on a ring) or authority (`forbidden`: an `authorize` that refused, M§3.6; only `retry()`
// or a reload helps).
func healsNever(err *data.ChordError) bool {
	return (err.Code == data.CodeInvalid && err.Reason == data.ReasonCycle) || err.Code == data.CodeForbidden
}

// parkedCause is what a reload reports for a parked attempt (slice 7b §4): its failure's cause, or for a blocker that is only
// loading a `waiting` cause naming it.
func parkedCause(parked check) data.FailureCause {
	if parked.shown.Kind == RefStateFailed {
		return parked.shown.Cause
	}
	label := "a dependency"
	if parked.loadingOn != nil {
		label = *parked.loadingOn
	}
	return data.WaitingFor(label, data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: label + " is loading"}, nil))
}

// waitingOn is a parked attempt's state for a blocker label that failed with err.
func waitingOn(label string, err *data.ChordError) *failed {
	return &RefState{Kind: RefStateFailed, Cause: data.WaitingFor(label, err), RetryAt: nil}
}

// parkedLong is the `parked_long` report of one parking episode (dependency model §7, audit A11): once, when the attempt has
// been parked longer than `readyTimeout`, whatever its blocker (a ring across hosts started at the same moment stays parked
// so, and so does a long wait; nothing fails or retries). The clock and the report's `since` start when the episode does
// (its pinned keys' loading included); the report names the blocker of the latest check. Its one timer runs until the
// report or the episode's end (`stop`). It is used inside the host's sections.
type parkedLong struct {
	// since is when the episode began (Date.now() time).
	since int64
	timer Timer
	// last is the blocker the latest check found; nil is Pi's undefined.
	last *blocker
}

func newParkedLong(loop eventLoop, object *chordObject) *parkedLong {
	p := &parkedLong{since: loop.Now()}
	p.timer = loop.AfterFunc(object.host.timeouts.ready, func() {
		found := p.last
		if found == nil {
			return
		}
		object.host.report(HostError{
			Kind: ErrParkedLong, Object: object.label(), WaitingFor: found.label, State: string(found.state), Since: p.since,
		})
	})
	return p
}

// blockedBy is what the latest check found blocking.
func (p *parkedLong) blockedBy(found blocker) { p.last = &found }

// stop: the episode ended: no report from now on.
func (p *parkedLong) stop() { p.timer.Stop() }
