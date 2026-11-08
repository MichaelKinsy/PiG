package host

import (
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// Ports packages/chord/src/host/reload-cycle.ts. Reload cycles (decision "Reload restarts every affected object; old work
// finishes on old code", rule 6): a wait of old work on an object whose activation waits, through its parked dependencies or
// the gates its opener waits at, on the old attempt that very work runs on. Nothing would end it but the reload drain bound,
// so it fails at once with `unavailable/reload_cycle`, naming the chain. Checked when the wait starts and again whenever an
// attempt parks or an opener starts waiting at a gate (a cycle may close after the wait began).
//
// Forced Go difference: every wait is of a `Promise<void>` (a getter's `live`, the gate), so `wait` takes and returns a
// `*keyedPromise[struct{}]`. All of it runs in a section of the host.

// oldWait is one wait of old work: on what, for which version, under which attempts (its call chain's), and how it is cut.
type oldWait struct {
	target   *chordObject
	contract *contracts.Contract
	chain    []oldWorkSource
	cut      func(err *data.ChordError)
}

// reloadCycles is a host's waits of old work on objects that are not live, while they last.
type reloadCycles struct {
	waits []*oldWait
}

// wait is waiting (a getter's or a gate's wait on target, for contract's version) as old work under chain makes it: rejected at
// once with `unavailable/reload_cycle` if it closes a cycle through that work's own drain, now or once a later park closes it.
func (c *reloadCycles) wait(loop sectionLoop, waiting *keyedPromise[struct{}], target *chordObject, contract *contracts.Contract, chain []oldWorkSource) *keyedPromise[struct{}] {
	result := newKeyedPromise[struct{}](loop)
	wait := &oldWait{target: target, contract: contract, chain: chain}
	wait.cut = func(err *data.ChordError) {
		c.remove(wait)
		result.reject(err)
	}
	c.waits = append(c.waits, wait)
	waiting.then(func(_ struct{}, err error) {
		c.remove(wait)
		result.settle(struct{}{}, err)
	})
	c.check(wait)
	return result
}

// remove forgets wait.
func (c *reloadCycles) remove(wait *oldWait) {
	if at := slices.Index(c.waits, wait); at >= 0 {
		c.waits = slices.Delete(c.waits, at, at+1)
	}
}

// recheck: an attempt parked, or an opener began waiting at a gate: every wait is checked again.
func (c *reloadCycles) recheck() {
	if len(c.waits) == 0 {
		return
	}
	for _, wait := range slices.Clone(c.waits) {
		c.check(wait)
	}
}

func (c *reloadCycles) check(wait *oldWait) {
	if !slices.Contains(c.waits, wait) {
		return
	}
	cycle := cycleFrom(wait.target, wait.contract, wait.chain)
	if cycle == nil {
		return
	}
	message := wait.target.label() + ": work its reload drains waits for it, and it waits for that drain (" + strings.Join(cycle, " → ") + ")"
	wait.cut(data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Reason: data.ReasonReloadCycle, Message: message}, nil))
}

// cycleFrom is the labels of a chain of waits from start to an object whose open old attempt is in chain; nil when there is
// none. The start itself counts only through its edges: if its open old attempt serves contract's version, the old work is
// routed there (or sees it live) and does not wait at all.
func cycleFrom(start *chordObject, contract *contracts.Contract, chain []oldWorkSource) []string {
	if start.oldServes(contract.Version, contract.Fingerprint) {
		return nil
	}
	visited := map[*chordObject]struct{}{start: {}}
	var walk func(object *chordObject) []string
	walk = func(object *chordObject) []string {
		for _, next := range object.waitEdges() {
			if _, seen := visited[next]; seen {
				continue
			}
			visited[next] = struct{}{}
			if old := next.openOldAttempt(); old != nil && slices.Contains(chain, oldWorkSource(old)) {
				return []string{next.label()}
			}
			if rest := walk(next); rest != nil {
				return append([]string{next.label()}, rest...)
			}
		}
		return nil
	}
	rest := walk(start)
	if rest == nil {
		return nil
	}
	return append([]string{start.label()}, rest...)
}
