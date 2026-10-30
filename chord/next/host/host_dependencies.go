package host

import (
	"time"

	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Ports packages/chord/src/next/host/dependencies.ts. Parked attempts (dependency model §3, §7): the dependency check, what
// an attempt's declared dependencies (`self.get` references and `self.registry` values) say at one moment: all live, or what
// the parked attempt shows while one is not (`loading`, `failed { waiting }` naming the nearest blocker and the root cause,
// or `broken` for a cycle, authority or an ended dependency), whether this host's first pass can count it (no blocker here
// still without its first outcome, decision (a)), and the remote blocker that is only loading; and the `parked_long` report
// a long wait on that blocker makes.

// dependencyTarget is what a binding's target says of the object that answers for it (Pi's `Target`, which `reference.ts`
// declares): D5a's `Target` satisfies it.
type dependencyTarget interface {
	// BlocksFirstPass: a blocker here that is still without its first outcome.
	BlocksFirstPass() bool
	// HeldOnLink: its hold is on a link.
	HeldOnLink() bool
	// Label names it in a report.
	Label() string
}

// dependencyBinding is a reference's binding as the check reads it (Pi's `Binding`): D5a's `*Binding` satisfies it.
type dependencyBinding interface {
	// refState is the reference's state now.
	refState() *RefState
	// label names the reference for what waits on it.
	label() string
	// target is what answers for it; nil is Pi's undefined (a binding not yet placed).
	target() dependencyTarget
}

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
	binding dependencyBinding
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

// check is what the dependencies say, by kind. Only the fields of its kind are set. shown, firstPass and remoteLoading are
// of a parked check: what the attempt shows, whether its first pass is over (no blocker, loading or failed, is an object
// here still without its first outcome), and a remote blocker whose hold is on a link and only loading (its label, nil
// when none), for `parked_long`. err is of a broken check.
type check struct {
	kind          checkKind
	shown         *RefState
	firstPass     bool
	remoteLoading *string
	err           *data.ChordError
}

// checkDependencies checks dependencies in declaration order (the first failed one is the one the attempt names).
func checkDependencies(dependencies []dependency) check {
	var firstFailed *failed
	firstPass := true
	var remoteLoading *string
	loading := false
	for _, dependency := range dependencies {
		if dependency.kind == dependencyRegistry {
			value := dependency.value
			start := value.Start()
			if start.Kind == state.StartValue || start.HasLast {
				continue
			}
			if firstFailed == nil {
				label := "registry " + value.Name
				firstFailed = waitingOn(label, data.AsChordError(start.Error, label))
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
			// Parked `waiting` on something here still in its first attempt: no first outcome yet (fix round 1).
			if target := binding.target(); target != nil && target.BlocksFirstPass() {
				firstPass = false
			}
		case RefStateLoading:
			loading = true
			target := binding.target()
			if target != nil && target.BlocksFirstPass() {
				firstPass = false
			}
			if target != nil && target.HeldOnLink() && remoteLoading == nil {
				label := target.Label()
				remoteLoading = &label
			}
		}
	}
	if firstFailed == nil && !loading {
		return check{kind: checkLive}
	}
	shown := refLoading
	if firstFailed != nil {
		shown = firstFailed
	}
	return check{kind: checkParked, shown: shown, firstPass: firstPass, remoteLoading: remoteLoading}
}

// healsNever reports whether a dependency that failed with err never heals by itself, so its dependent breaks too: a cycle
// (`invalid/cycle`: it would wait on a ring) or authority (`forbidden`: an `authorize` that refused, M§3.6; only `retry()`
// or a reload helps).
func healsNever(err *data.ChordError) bool {
	return (err.Code == data.CodeInvalid && err.Reason == data.ReasonCycle) || err.Code == data.CodeForbidden
}

// waitingOn is a parked attempt's state for a blocker label that failed with err.
func waitingOn(label string, err *data.ChordError) *failed {
	return &RefState{Kind: RefStateFailed, Cause: data.WaitingFor(label, err), RetryAt: nil}
}

// parkedLongObject is the object whose attempt parks, as ParkedLong reads it (Pi's `ChordObject` and its `host`): D5a's
// `*ChordObject` satisfies it.
type parkedLongObject interface {
	// label is the object's label, which a report names it by.
	label() string
	// readyTimeout is its host's `timeouts.ready`.
	readyTimeout() time.Duration
	// report is its host's `report`.
	report(HostError)
}

// parkedLong is the `parked_long` report of one parking episode (dependency model §7): once, when the attempt has waited
// `readyTimeout` on a remote blocker that is only loading with its hold on a link (a ring across hosts started at the same
// moment stays parked so; nothing fails or retries). The clock, and the report's `since`, start when such a blocker is
// there and stop when none is (a link that drops, a path still without one): only the time a remote attempt answered for
// the wait counts. Its one timer runs only meanwhile. It is used inside the host's sections.
type parkedLong struct {
	loop   eventLoop
	object parkedLongObject
	// since is when the attempt began to wait on a remote blocker that is only loading on a link, while it does (Date.now()
	// time); nil is Pi's undefined.
	since *int64
	timer Timer
	// blocker is the remote blocker it would name now; nil is Pi's undefined.
	blocker  *string
	reported bool
}

func newParkedLong(loop eventLoop, object parkedLongObject) *parkedLong {
	return &parkedLong{loop: loop, object: object}
}

// waitingOn is what the check found: blocker, a remote attempt that is only loading on a link, or none (nil).
func (p *parkedLong) waitingOn(blocker *string) {
	p.blocker = blocker
	if blocker == nil {
		p.stop()
		return
	}
	if p.reported || p.timer != nil {
		return
	}
	if p.since == nil {
		now := p.loop.Now()
		p.since = &now
	}
	due := time.Duration(*p.since-p.loop.Now())*time.Millisecond + p.object.readyTimeout()
	p.timer = p.loop.AfterFunc(max(0, due), p.report)
}

// stop: the episode ended, or no remote blocker is loading on a link now: the clock and its timer stop.
func (p *parkedLong) stop() {
	if p.timer != nil {
		p.timer.Stop()
	}
	p.timer = nil
	p.since = nil
}

func (p *parkedLong) report() {
	p.timer = nil
	waitingFor := p.blocker
	since := p.since
	if p.reported || waitingFor == nil || since == nil {
		return
	}
	p.reported = true
	p.object.report(HostError{Kind: ErrParkedLong, Object: p.object.label(), WaitingFor: *waitingFor, Since: *since})
}
